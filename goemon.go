package goemon

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/omeid/livereload"
	"gopkg.in/fsnotify.v1"
	"gopkg.in/yaml.v2"
)

const logFlag = log.Ldate | log.Ltime | log.Lshortfile

var commandRe = regexp.MustCompile(`^\s*(:[a-z]+)(?:\s+(\S+))*$`)

type goemon struct {
	File   string
	Logger *log.Logger
	Args   []string
	lrc    net.Listener
	lrs    *livereload.Server
	fsw    *fsnotify.Watcher
	cmd    *exec.Cmd
	conf   conf
	asset  func(string) ([]byte, error)
}

type task struct {
	Match    string   `yaml:"match"`
	Ignore   string   `yaml:"ignore"`
	Commands []string `yaml:"commands"`
	mre      *regexp.Regexp
	ire      *regexp.Regexp
	hit      bool
	mutex    sync.Mutex
}

type conf struct {
	Command    string
	LiveReload string  `yaml:"livereload"`
	Tasks      []*task `yaml:"tasks"`
}

//New create new instance of goemon
func New() *goemon {
	return &goemon{
		File:   "goemon.yml",
		Logger: log.New(os.Stderr, "GOEMON ", logFlag),
	}
}

//New create new instance of goemon with specified arguments by args
func NewWithArgs(args []string) *goemon {
	g := New()
	g.Args = args
	return g
}

//Run start goemon server
func Run() *goemon {
	return New().Run()
}

func compilePattern(pattern string) (*regexp.Regexp, error) {
	if pattern[0] == '%' {
		return regexp.Compile(pattern[1:])
	}

	var buf bytes.Buffer

	for n, pat := range strings.Split(pattern, "|") {
		if n == 0 {
			buf.WriteString("^")
		} else {
			buf.WriteString("$|")
		}
		if fs, err := filepath.Abs(pat); err == nil {
			pat = filepath.ToSlash(fs)
		}
		rs := []rune(pat)
		for i := 0; i < len(rs); i++ {
			if rs[i] == '/' {
				if runtime.GOOS == "windows" {
					buf.WriteString(`[/\\]`)
				} else {
					buf.WriteRune(rs[i])
				}
			} else if rs[i] == '*' {
				if i < len(rs)-1 && rs[i+1] == '*' {
					i++
					if i < len(rs)-1 && rs[i+1] == '/' {
						i++
						buf.WriteString(`.*`)
					} else {
						return nil, fmt.Errorf("invalid wildcard: %s", pattern)
					}
				} else {
					buf.WriteString(`[^/]+`)
				}
			} else if rs[i] == '?' {
				buf.WriteString(`\S`)
			} else {
				buf.WriteString(fmt.Sprintf(`[\x%x]`, rs[i]))
			}
		}
		buf.WriteString("$")
	}

	return regexp.Compile(buf.String())
}

func (g *goemon) restart() error {
	if len(g.Args) == 0 {
		return nil
	}
	g.terminate()
	return g.spawn()
}

func (t *task) match(file string) bool {
	return (t.mre != nil && t.mre.MatchString(file)) && (t.ire == nil || !t.ire.MatchString(file))
}

func (g *goemon) task(event fsnotify.Event) {
	file := filepath.ToSlash(event.Name)
	for _, t := range g.conf.Tasks {
		if !t.match(file) {
			continue
		}
		t.mutex.Lock()
		if t.hit {
			t.mutex.Unlock()
			continue
		}
		t.hit = true
		t.mutex.Unlock()
		g.Logger.Println(event)
		go func(name string, t *task) {
		loopCommand:
			for _, command := range t.Commands {
				switch {
				case commandRe.MatchString(command):
					if !g.internalCommand(command, file) {
						break loopCommand
					}
				default:
					if !g.externalCommand(command, file) {
						break loopCommand
					}
				}
			}
			t.mutex.Lock()
			t.hit = false
			t.mutex.Unlock()
		}(event.Name, t)
	}
}

func (g *goemon) watch() error {
	var err error
	g.fsw, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	g.fsw.Add(g.File)

	root, err := filepath.Abs(".")
	if err != nil {
		g.Logger.Println(err)
	}

	dup := map[string]bool{}
	g.fsw.Add(root)
	dup[root] = true

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if info == nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if _, ok := dup[path]; !ok {
			g.fsw.Add(path)
			dup[path] = true
		}
		return nil
	})
	if err != nil {
		g.Logger.Println(err)
	}

	g.Logger.Println("goemon loaded", g.File)

	for {
		select {
		case event := <-g.fsw.Events:
			if event.Name == g.File {
				return nil
			}
			g.task(event)
		case err := <-g.fsw.Errors:
			if err != nil {
				g.Logger.Println("error:", err)
			}
		}
	}
}

func (g *goemon) load() error {
	g.conf.Tasks = []*task{}
	fn, err := filepath.Abs(g.File)
	if err != nil {
		return err
	}
	var b []byte
	g.File = fn
	if g.asset == nil {
		b, err = ioutil.ReadFile(fn)
	} else {
		b, err = g.asset(g.File)
	}
	if err != nil {
		return err
	}
	err = yaml.Unmarshal(b, &g.conf)
	if err != nil {
		return err
	}
	if len(g.Args) == 0 && g.conf.Command != "" {
		if runtime.GOOS == "windows" {
			g.Args = []string{"cmd", "/c", g.conf.Command}
		} else {
			g.Args = []string{"sh", "-c", g.conf.Command}
		}
	}
	for _, t := range g.conf.Tasks {
		if t.Match == "" {
			continue
		}
		t.mre, err = compilePattern(t.Match)
		if err != nil {
			g.Logger.Println(err)
			continue
		}
		if t.Ignore != "" {
			t.ire, err = compilePattern(t.Ignore)
			if err != nil {
				g.Logger.Println(err)
			}
		} else {
			t.ire = nil
		}
	}
	return nil
}

func (g *goemon) SetAsset(asset func(string) ([]byte, error)) {
	g.asset = asset
}

func (g *goemon) Run() *goemon {
	err := g.load()
	if err != nil {
		g.Logger.Println(err)
	}

	go func() {
		g.Logger.Println("loading", g.File)
		for {
			err := g.watch()
			if err != nil {
				g.Logger.Println(err)
				time.Sleep(time.Second)
			}
			g.Logger.Println("reloading", g.File)
			err = g.load()
			if err != nil {
				g.Logger.Println(err)
				time.Sleep(time.Second)
			}
		}
	}()

	go func() {
		g.Logger.Println("starting livereload")
		for {
			err := g.livereload()
			if err != nil {
				g.Logger.Println(err)
				time.Sleep(time.Second)
			}
			g.Logger.Println("restarting livereload")
		}
	}()

	if len(g.Args) > 0 {
		g.Logger.Println("starting command", g.Args)
		for {
			err := g.restart()
			if err != nil {
				g.Logger.Println(err)
				time.Sleep(time.Second)
			}
			g.Logger.Println("restarting command")
		}
	}
	return g
}

//Terminate stop goemon server
func (g *goemon) Terminate() {
	if g.lrc != nil {
		g.lrc.Close()
	}
	if g.fsw != nil {
		g.fsw.Close()
	}
	if g.cmd.Process != nil {
		g.terminate()
	}
	g.Logger.Println("goemon terminated")
}
