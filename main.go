package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/jpillora/ansi"
	"github.com/jpillora/opts"
	"golang.org/x/crypto/ssh/terminal"
)

var version = "0.5.0" //set via ldflags

var (
	//config
	config = struct {
		Keep        bool     `help:"keep duplicates (by default, duplicates are deleted)"`
		Merge       bool     `help:"move unique files into the first directory"`
		Recursive   bool     `help:"searches into nested directories"`
		Verbose     bool     `help:"verbose logs (displays each move and delete)"`
		Dryrun      bool     `help:"runs exactly as configured, except no changes are made"`
		Workers     int      `help:"number of worker threads"`
		Hash        string   `help:"hashing algorithm to use" default:"md5, can also choose sha1 and sha256"`
		Directories []string `type:"arg" min:"1"`
	}{
		Workers: runtime.NumCPU(),
		Hash:    "md5",
	}
	//path separator
	sep = string(filepath.Separator)
	//indexMut guards index
	indexMut = sync.Mutex{}
	index    = map[string]*indexEntry{}
	//stats
	hashed  = uint64(0)
	deletes = uint64(0)
	moves   = uint64(0)
)

const summary = `deduplicates all files in the provided directories, while optionally merging
them into the first directory. The merge operation renames files (when a path
collision occurs).`

const notes = `
Notes:
* dedup considers two files duplicates if they have matching hash sums.
* dedup is a destructive operation (unless --keep).
* dedup on a single directory will only perform deduplication, no moves.
* dedup renames: when a file is unique, dedup will attempt to move the file.
  if the path already exists the incoming file will be suffixed with the next
  number (for example, if 'foo.txt' exists, the new file will be 'foo-2.txt').
* enabling --keep and not --merge is a read-only operation.
* any error will cause dedup to exit.
`

func main() {
	//cli
	o := opts.New(&config)
	o.Summary(summary)
	o.DocBefore("version", "notes", notes)
	o.Version(version)
	o.Repo("github.com/jpillora/dedup")
	o.Parse()
	//validate hash
	switch config.Hash {
	case "md5":
		newHash = func() hash.Hash {
			return md5.New()
		}
	case "sha1":
		newHash = func() hash.Hash {
			return sha1.New()
		}
	case "sha256":
		newHash = func() hash.Hash {
			return sha256.New()
		}
	default:
		check(errors.New("Unknown hashing algorithm"))
	}
	//validate dirs
	dirs := config.Directories
	for i, d := range dirs {
		d = strings.TrimSuffix(d, sep)
		info, err := os.Stat(d)
		check(err, "stat-input: "+d)
		if !info.IsDir() {
			check(errors.New("Must be directory"))
		}
		dirs[i] = d
	}
	//run!
	dst := dirs[0]
	for _, src := range dirs {
		scan(dst, src, dirs)
	}
	//done!
	printf("Done")
}

func scan(dst, src string, inputDirs []string) {
	first := dst == src
	noMerge := first || !config.Merge
	queue := newQueue()
	//dequeue until the queue is empty
	queue.de(func(path string) {
		info, err := os.Stat(path)
		check(err, "stat-dequeue: "+path)
		//directory? en all files
		if info.IsDir() {
			//ignore subdirs of path unless --recursive
			if src != path && !config.Recursive {
				return
			}
			//recurse!
			if config.Verbose {
				printf("Scanning %s", blue(display(path)))
			}
			files, err := ioutil.ReadDir(path)
			check(err, "read-dir: "+path)
			paths := []string{}
			for _, info := range files {
				p := filepath.Join(path, info.Name())
				//if listed as another input dir,
				//skip to provide control of process order.
				isInput := false
				for _, id := range inputDirs {
					if src != id && p == id {
						isInput = true
					}
				}
				if isInput {
					continue
				}
				//include!
				paths = append(paths, p)
			}
			queue.en(paths)
			return
		}
		//abnormal or hidden file? skip
		if !info.Mode().IsRegular() || strings.HasPrefix(filepath.Base(path), ".") {
			return
		}
		//normal file, hash!
		hex := hashFile(path)
		atomic.AddUint64(&hashed, 1)
		//lookup, decide and update atomically
		indexMut.Lock()
		defer indexMut.Unlock()
		existing, exists := index[hex]
		if exists {
			if path == existing.path {
				return //already scanned
			}
			//files from earlier input dirs always win, otherwise
			//keep the preferred one of the two (see keepFirst)
			if config.Keep || existing.src != src || !keepFirst(path, existing.path) {
				if !config.Keep {
					removeDupe(path, path, existing.path)
				}
				return
			}
			//this file wins, delete the previously indexed one
			removeDupe(existing.current, existing.path, path)
		}
		//file is unqiue (or replaces a dupe)! place in the index
		entry := &indexEntry{src: src, path: path, current: path}
		index[hex] = entry
		//merge unique files into first directory?
		if noMerge {
			return
		}
		//find next availbe file name
		name := info.Name()
		dstpath := filepath.Join(dst, name)
		n := 1
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		for {
			if _, err := os.Stat(dstpath); os.IsNotExist(err) {
				break
			}
			n++
			newname := fmt.Sprintf("%s-%d%s", base, n, ext)
			dstpath = filepath.Join(dst, newname)
		}
		//missing, move in
		if config.Verbose {
			t, s, d := trimPathPrefix(path, dstpath)
			if t == "" {
				printf("Moving: %s -> %s", s, green(d))
			} else {
				printf("Moving: %s/{%s -> %s}", grey(t), s, green(d))
			}
		}
		if !config.Dryrun {
			check(os.Rename(path, dstpath))
		}
		entry.current = dstpath
		atomic.AddUint64(&moves, 1)
	})
	//initial item
	queue.en([]string{src})
	//wait for queue to empty
	queue.wait()
}

//indexEntry is the kept file for a given hash
type indexEntry struct {
	src     string //input directory the file was found in
	path    string //path where the file was found
	current string //path where the file is now (differs after a merge move)
}

//keepFirst reports whether file a should be kept over its duplicate b
//(both from the same input directory):
//  1. the file in the deeper subfolder is kept
//  2. in the same directory, the file with the longer name is kept
//  3. otherwise, the lexicographically smaller path is kept
func keepFirst(a, b string) bool {
	if da, db := strings.Count(a, sep), strings.Count(b, sep); da != db {
		return da > db
	}
	if filepath.Dir(a) == filepath.Dir(b) {
		la := utf8.RuneCountInString(filepath.Base(a))
		lb := utf8.RuneCountInString(filepath.Base(b))
		if la != lb {
			return la > lb
		}
	}
	return a < b
}

//removeDupe deletes the file at del (found at delPath), a dupe-of exiPath
func removeDupe(del, delPath, exiPath string) {
	if config.Verbose {
		t, d, e := trimPathPrefix(delPath, exiPath)
		if t == "" {
			printf("Removing %s dupe-of %s", red(d), blue(e))
		} else {
			printf("Removing %s/{%s dupe-of %s}", grey(t), red(d), blue(e))
		}
	}
	if !config.Dryrun {
		check(os.Remove(del))
	}
	atomic.AddUint64(&deletes, 1)
}

type queue struct {
	wg     sync.WaitGroup
	queue  chan string
	closed bool
	count  uint64
	total  uint64
}

func newQueue() *queue {
	return &queue{
		queue: make(chan string),
	}
}

func (p *queue) en(paths []string) {
	atomic.AddUint64(&p.total, uint64(len(paths)))
	go func() {
		for _, path := range paths {
			p.queue <- path
		}
	}()
}

func (p *queue) de(fn func(path string)) {
	work := func() {
		for f := range p.queue {
			fn(f)
			//bump up completed counter
			atomic.AddUint64(&p.count, 1)
			//close one there are no more items
			count := atomic.LoadUint64(&p.count)
			total := atomic.LoadUint64(&p.total)
			if count == total {
				close(p.queue)
			}
			if config.Verbose && count%100 == 0 {
				printf(grey("Performed #%d actions with #%d queued"), count, total-count)
			}
		}
		p.wg.Done()
	}
	for i := 0; i < config.Workers; i++ {
		p.wg.Add(1)
		go work()
	}
}

func (p *queue) wait() {
	p.wg.Wait()
}

func (p *queue) close() {
	if !p.closed {
		close(p.queue)
		p.closed = true
	}
}

var isaTTY = terminal.IsTerminal(int(os.Stdout.Fd()))

func color(attr ansi.Attribute) func(string) string {
	if !isaTTY {
		return func(s string) string {
			return s
		}
	}
	col := string(ansi.Set(attr))
	reset := string(ansi.ResetBytes)
	return func(s string) string {
		return col + s + reset
	}
}

var grey = color(ansi.Black)
var green = color(ansi.Green)
var red = color(ansi.Red)
var blue = color(ansi.Blue)

func report() string {
	parts := []string{}
	h := atomic.LoadUint64(&hashed)
	if h > 0 {
		parts = append(parts, fmt.Sprintf("hashed %d", h))
	}
	m := atomic.LoadUint64(&moves)
	if m > 0 {
		parts = append(parts, fmt.Sprintf("moved %d", m))
	}
	d := atomic.LoadUint64(&deletes)
	if d > 0 {
		parts = append(parts, fmt.Sprintf("deleted %d", d))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

var lastReport = ""

func printf(format string, args ...interface{}) {
	if config.Dryrun {
		format = grey("[DRYRUN] ") + format
	}
	r := report()
	if r != lastReport {
		format += grey(" (" + r + ")")
		lastReport = r
	}
	format += "\n"
	fmt.Printf(format, args...)
}

type hashFactory func() hash.Hash

var newHash hashFactory

//hash the file at path to an sha1 hex string
func hashFile(p string) string {
	f, err := os.Open(p)
	check(err, p)
	h := newHash()
	io.Copy(h, f)
	f.Close()
	return hex.EncodeToString(h.Sum(nil))
}

//halt program if error is encountered
func check(err error, msg ...string) {
	if err != nil {
		format := "Error: %s"
		if len(msg) > 0 {
			format += " [" + strings.Join(msg, " ") + "]"
		}
		format += " (%s)"
		fmt.Fprintf(os.Stderr, format, err, report())
		os.Exit(1)
	}
}

func display(path string) string {
	return strings.Replace(path, " ", "·", -1)
}

func contains(set []string, item string) bool {
	for _, s := range set {
		if s == item {
			return true
		}
	}
	return false
}

func trimPathPrefix(pathA, pathB string) (string, string, string) {
	partsT := []string{}
	partsA := strings.Split(pathA, sep)
	partsB := strings.Split(pathB, sep)
	for len(partsA) > 0 && len(partsB) > 0 {
		a := partsA[0]
		b := partsB[0]
		if a != b {
			break
		}
		partsT = append(partsT, a)
		partsA = partsA[1:]
		partsB = partsB[1:]
	}
	pathTrimmed := strings.Join(partsT, sep)
	pathA = strings.Join(partsA, sep)
	pathB = strings.Join(partsB, sep)
	return display(pathTrimmed), display(pathA), display(pathB)
}
