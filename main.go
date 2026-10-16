// desksynk shares one keyboard and mouse between a Linux (Hyprland) machine
// and a Mac.
package main

import (
	"flag"
	"fmt"
	"os"
)

func usage() {
	fmt.Fprint(os.Stderr, `desksynk: share one keyboard and mouse between Linux and macOS

  desksynk server [flags]        on the Linux machine with the keyboard and mouse
  desksynk client [flags] HOST   on the Mac

Run "desksynk server -h" or "desksynk client -h" for flags.
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "server":
		fs := flag.NewFlagSet("server", flag.ExitOnError)
		o := serverOpts{}
		fs.StringVar(&o.listen, "listen", "0.0.0.0:24810", "address to listen on")
		fs.StringVar(&o.side, "mac-side", "left", "which side of the Linux screens the Mac sits on: left, right, top, bottom")
		fs.BoolVar(&o.noClipboard, "no-clipboard", false, "don't sync clipboard text")
		fs.Parse(os.Args[2:])
		err = runServer(o)
	case "client":
		fs := flag.NewFlagSet("client", flag.ExitOnError)
		o := clientOpts{}
		fs.Float64Var(&o.speed, "speed", 1.0, "pointer speed multiplier")
		fs.BoolVar(&o.naturalScroll, "natural-scroll", false, "invert scroll direction")
		fs.BoolVar(&o.altIsCmd, "alt-is-cmd", false, "Alt acts as Command and Super as Option (Mac key positions)")
		fs.BoolVar(&o.noClipboard, "no-clipboard", false, "don't sync clipboard text")
		fs.Parse(os.Args[2:])
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: desksynk client [flags] HOST[:PORT]")
			os.Exit(2)
		}
		o.addr = fs.Arg(0)
		err = runClient(o)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "desksynk:", err)
		os.Exit(1)
	}
}

type serverOpts struct {
	listen, side string
	noClipboard  bool
	onlyDevice   string // tests: use just this device name
}

type clientOpts struct {
	addr                                 string
	speed                                float64
	naturalScroll, altIsCmd, noClipboard bool
}
