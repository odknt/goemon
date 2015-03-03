package main

import (
	"fmt"
	"github.com/mattn/goemon"
	"os"
)

var defaultConf = `# Generated by goemon -g
livereload: :35730
tasks:
- match: './assets/*.js'
  commands:
  - minifyjs -m -i ${GOEMON_TARGET_FILE} > ${GOEMON_TARGET_DIR}/${GOEMON_TARGET_NAME}.min.js
  - :livereload /
- match: './assets/*.css'
  commands:
  - :livereload /
- match: './assets/*.html'
  commands:
  - :livereload /
- match: '*.go'
  commands:
  - go build
  - :restart
  - :livereload /
`

func usage() {
	fmt.Printf("Usage of %s [options] [command] [args...]\n", os.Args[0])
	fmt.Println(" goemon -g : generate default configuration")
	fmt.Println(" goemon -c [FILE] ... : set configuration file")
	os.Exit(1)
}

func main() {
	file := ""
	args := []string{}

	switch len(os.Args) {
	case 1:
		usage()
	default:
		switch os.Args[1] {
		case "-h":
			usage()
		case "-g":
			fmt.Println(defaultConf)
			return
		case "-c":
			if len(os.Args) == 2 {
				usage()
				return
			}
			file = os.Args[2]
			args = os.Args[3:]
		case "--":
			args = os.Args[2:]
		default:
			args = os.Args[1:]
		}
	}

	g := goemon.NewWithArgs(args)
	if file != "" {
		g.File = file
	}
	g.Run()
}
