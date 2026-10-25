package main

// tools/inspect is a development helper: it prints the metadata AllTag parses
// from the files given on the command line. It is not part of the shipped app.

import (
	"encoding/json"
	"fmt"
	"os"

	"alltag/internal/audiometa"
)

func main() {
	for _, p := range os.Args[1:] {
		t, err := audiometa.Read(p)
		if err != nil {
			fmt.Printf("%-34s ERROR %v\n", p, err)
			continue
		}
		meta := map[string]any{
			"format": t.Format, "tag": t.TagType,
			"dur": round(t.Info.Duration), "kbps": t.Info.Bitrate,
			"sr": t.Info.SampleRate, "ch": t.Info.Channels,
			"bits": t.Info.BitDepth, "codec": t.Info.Codec,
			"pics": len(t.Pictures), "fields": t.Fields,
		}
		b, _ := json.Marshal(meta)
		fmt.Printf("%-34s %s\n", p, string(b))
	}
}

func round(f float64) float64 {
	return float64(int(f*100)) / 100
}
