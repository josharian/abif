// +build ignore

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/foggyco/x/abif"
)

var (
	printValues  = flag.Bool("val", true, "print values")
	useKnownTags = flag.Bool("k", true, "use known tags to interpret data")
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: %s <file>\n", os.Args[0])
		os.Exit(2)
	}
	src, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	r, err := abif.NewReader(src)
	if err != nil {
		log.Fatal(err)
	}

	tags := r.Tags()
	sort.Sort(tagsByString(tags))
	for _, t := range tags {
		v, err := r.Value(t)
		if err != nil {
			fmt.Printf("%s: %v\n", t, err)
		} else {
			if *printValues {
				if *useKnownTags {
					switch string(t.Name[:]) {
					case "APrX", "PBAS", "RMdX", "FWO_":
						c := v.([]int8)
						b := make([]byte, len(c))
						for i := range c {
							b[i] = byte(c[i])
						}
						v = string(b)
					}
				}
				// PBAS 1/2: seq characters edited by user, basecaller
				// PCON 1/2: quality values ...
				// PLOC 1/2: peak locations ...
				fmt.Printf("%s: %T(%v)\n", t, v, v)
			} else {
				fmt.Printf("%s: %T\n", t, v)
			}
		}
	}
}

type tagsByString []abif.Tag

func (x tagsByString) Len() int      { return len(x) }
func (x tagsByString) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x tagsByString) Less(i, j int) bool {
	iname := string(x[i].Name[:])
	jname := string(x[j].Name[:])
	if iname != jname {
		return iname < jname
	}
	return x[i].Num < x[j].Num
}
