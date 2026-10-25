package main

// tools/tagwrite is a development helper used by the verification scripts: it
// writes a fixed set of tags (and an optional cover) into the given files.

import (
	"flag"
	"fmt"
	"log"
	"os"

	"alltag/internal/audiometa"
)

func main() {
	art := flag.String("art", "", "cover image to embed")
	del := flag.Bool("delete", false, "remove all tags instead")
	flag.Parse()
	var cover []byte
	if *art != "" {
		b, err := os.ReadFile(*art)
		if err != nil {
			log.Fatal(err)
		}
		cover = b
	}
	for _, p := range flag.Args() {
		e := audiometa.Edits{Set: map[string]string{
			"TITLE":       "Ünïcode Tïtle ✓",
			"ARTIST":      "Artist One",
			"ALBUM":       "Album X",
			"YEAR":        "2004",
			"TRACK":       "7/12",
			"GENRE":       "Electronic",
			"COMMENT":     "written by AllTag",
			"ALBUMARTIST": "Various",
			"COMPOSER":    "Comp Oser",
			"BPM":         "128",
		}}
		if *del {
			for k := range e.Set {
				e.Set[k] = ""
			}
			e.RemoveAllPictures = true
		}
		if len(cover) > 0 {
			e.PictureAction = "set"
			e.Picture = cover
			e.PictureMIME = "image/png"
		}
		if err := audiometa.ApplyEdits(p, e); err != nil {
			fmt.Printf("FAIL %s: %v\n", p, err)
			os.Exit(1)
		}
		fmt.Printf("ok %s\n", p)
	}
}
