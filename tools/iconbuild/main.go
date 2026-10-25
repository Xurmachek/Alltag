// Command iconbuild renders the AllTag application icon and embeds it — plus a
// Windows manifest — into a COFF object file that the Go linker picks up.
//
// Run from the repository root:
//
//	go run ./tools/iconbuild
//
// It writes assets/icon.ico and rsrc_windows_amd64.syso. The resource section
// is a plain resource directory; the Go linker copies it verbatim and points
// the PE data directory at it.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------- raster

type canvas struct {
	w, h int
	pix  []color.NRGBA
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, pix: make([]color.NRGBA, w*h)}
	return c
}

func (c *canvas) set(x, y int, col color.NRGBA) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	c.pix[y*c.w+x] = col
}

// shape reports how much of a sub-pixel sample is covered.
type shape func(u, v float64) bool

func roundRectShape(x0, y0, x1, y1, r float64) shape {
	return func(u, v float64) bool {
		if u < x0 || u > x1 || v < y0 || v > y1 {
			return false
		}
		cx := clamp(u, x0+r, x1-r)
		cy := clamp(v, y0+r, y1-r)
		dx, dy := u-cx, v-cy
		return dx*dx+dy*dy <= r*r
	}
}

func circleShape(cx, cy, r float64) shape {
	return func(u, v float64) bool {
		dx, dy := u-cx, v-cy
		return dx*dx+dy*dy <= r*r
	}
}

// quadShape is a convex quadrilateral given clockwise.
func quadShape(pts [4][2]float64) shape {
	return func(u, v float64) bool {
		for i := 0; i < 4; i++ {
			a, b := pts[i], pts[(i+1)%4]
			if (b[0]-a[0])*(v-a[1])-(b[1]-a[1])*(u-a[0]) < 0 {
				return false
			}
		}
		return true
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func mixColor(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		R: uint8(lerp(float64(a.R), float64(b.R), t)),
		G: uint8(lerp(float64(a.G), float64(b.G), t)),
		B: uint8(lerp(float64(a.B), float64(b.B), t)),
		A: uint8(lerp(float64(a.A), float64(b.A), t)),
	}
}

// render draws the AllTag mark: a rounded gradient tile with a beamed note.
func render(size int) *canvas {
	c := newCanvas(size, size)
	s := float64(size)

	const ss = 4 // supersampling grid
	steps := ss * ss
	inv := 1.0 / float64(steps)

	tile := roundRectShape(0.02*s, 0.02*s, 0.98*s, 0.98*s, 0.26*s)
	headA := circleShape(0.365*s, 0.705*s, 0.105*s)
	headB := circleShape(0.615*s, 0.665*s, 0.105*s)
	stemA := roundRectShape(0.415*s, 0.30*s, 0.475*s, 0.705*s, 0.028*s)
	stemB := roundRectShape(0.665*s, 0.26*s, 0.725*s, 0.665*s, 0.028*s)
	beam := quadShape([4][2]float64{
		{0.415 * s, 0.30 * s},
		{0.725 * s, 0.26 * s},
		{0.725 * s, 0.385 * s},
		{0.415 * s, 0.425 * s},
	})
	note := func(u, v float64) bool {
		return headA(u, v) || headB(u, v) || stemA(u, v) || stemB(u, v) || beam(u, v)
	}

	top := color.NRGBA{0xDA, 0xCB, 0xFF, 0xFF}
	bottom := color.NRGBA{0x8B, 0x6F, 0xD8, 0xFF}
	ink := color.NRGBA{0x16, 0x14, 0x1C, 0xFF}

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var tileCov, noteCov int
			var accR, accG, accB float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := float64(x) + (float64(sx)+0.5)/ss
					v := float64(y) + (float64(sy)+0.5)/ss
					if !tile(u, v) {
						continue
					}
					tileCov++
					t := clamp((u/s+v/s)/2, 0, 1)
					base := mixColor(top, bottom, t)
					if note(u, v) {
						noteCov++
						accR += float64(ink.R)
						accG += float64(ink.G)
						accB += float64(ink.B)
					} else {
						accR += float64(base.R)
						accG += float64(base.G)
						accB += float64(base.B)
					}
				}
			}
			if tileCov == 0 {
				continue
			}
			alpha := float64(tileCov) * inv
			col := color.NRGBA{
				R: uint8(accR / float64(tileCov)),
				G: uint8(accG / float64(tileCov)),
				B: uint8(accB / float64(tileCov)),
				A: uint8(alpha * 255),
			}
			// premultiplied average: renormalise so edges stay crisp
			if noteCov > 0 && noteCov < tileCov {
				col = mixColor(color.NRGBA{
					R: uint8(accR / float64(tileCov)),
					G: uint8(accG / float64(tileCov)),
					B: uint8(accB / float64(tileCov)),
					A: col.A,
				}, col, 0)
			}
			c.set(x, y, col)
		}
	}
	return c
}

// ---------------------------------------------------------------- ICO

func encodeBMPIcon(c *canvas) []byte {
	// BITMAPINFOHEADER + BGRA rows bottom-up + 1bpp AND mask.
	w, h := c.w, c.h
	var buf bytes.Buffer
	buf.Write([]byte{40, 0, 0, 0})
	binary.Write(&buf, binary.LittleEndian, int32(w))
	binary.Write(&buf, binary.LittleEndian, int32(h*2))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // BI_RGB
	binary.Write(&buf, binary.LittleEndian, uint32(w*h*4))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			p := c.pix[y*w+x]
			buf.Write([]byte{p.B, p.G, p.R, p.A})
		}
	}
	maskRow := ((w + 31) / 32) * 4
	for y := 0; y < h; y++ {
		row := make([]byte, maskRow)
		for x := 0; x < w; x++ {
			if c.pix[y*w+x].A == 0 {
				row[x/8] |= 0x80 >> (uint(x) % 8)
			}
		}
		buf.Write(row)
	}
	return buf.Bytes()
}

func encodePNGIcon(c *canvas) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, c.w, c.h))
	for i, p := range c.pix {
		img.Pix[i*4+0] = p.R
		img.Pix[i*4+1] = p.G
		img.Pix[i*4+2] = p.B
		img.Pix[i*4+3] = p.A
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

type iconImage struct {
	size int
	data []byte
}

func buildIcons(sizes []int) []iconImage {
	var out []iconImage
	for _, s := range sizes {
		c := render(s)
		var data []byte
		if s >= 64 {
			data = encodePNGIcon(c)
		} else {
			data = encodeBMPIcon(c)
		}
		out = append(out, iconImage{size: s, data: data})
	}
	return out
}

func writeICO(path string, icons []iconImage) error {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type = icon
	binary.Write(&buf, binary.LittleEndian, uint16(len(icons)))
	offset := 6 + 16*len(icons)
	for _, ic := range icons {
		dim := byte(ic.size)
		if ic.size >= 256 {
			dim = 0
		}
		buf.Write([]byte{dim, dim, 0, 0})
		binary.Write(&buf, binary.LittleEndian, uint16(1))
		binary.Write(&buf, binary.LittleEndian, uint16(32))
		binary.Write(&buf, binary.LittleEndian, uint32(len(ic.data)))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(ic.data)
	}
	for _, ic := range icons {
		buf.Write(ic.data)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func groupIcon(icons []iconImage, ids []int) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(len(icons)))
	for i, ic := range icons {
		dim := byte(ic.size)
		if ic.size >= 256 {
			dim = 0
		}
		buf.Write([]byte{dim, dim, 0, 0})
		binary.Write(&buf, binary.LittleEndian, uint16(1))
		binary.Write(&buf, binary.LittleEndian, uint16(32))
		binary.Write(&buf, binary.LittleEndian, uint32(len(ic.data)))
		binary.Write(&buf, binary.LittleEndian, uint16(ids[i]))
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------- resources

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtManifest  = 24
)

type resource struct {
	typ  uint32
	name uint32
	data []byte
}

// buildResourceSection lays out a resource directory tree. Every offset stored
// in the tree is relative to the start of the section, which is exactly what
// Windows expects once the PE data directory points here.
func buildResourceSection(res []resource) []byte {
	// group by type
	types := []uint32{}
	byType := map[uint32][]resource{}
	for _, r := range res {
		if _, ok := byType[r.typ]; !ok {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], r)
	}
	sortUint32(types)

	dirSize := func(n int) int { return 16 + 8*n }
	dataEntrySize := 16

	// layout: root dir, type dirs, name dirs, data entries, data blobs
	off := dirSize(len(types))
	typeDirOff := map[uint32]int{}
	for _, t := range types {
		typeDirOff[t] = off
		off += dirSize(len(byType[t]))
	}
	nameDirOff := map[[2]uint32]int{}
	for _, t := range types {
		for _, r := range byType[t] {
			nameDirOff[[2]uint32{t, r.name}] = off
			off += dirSize(1)
		}
	}
	dataEntryOff := map[[2]uint32]int{}
	for _, t := range types {
		for _, r := range byType[t] {
			dataEntryOff[[2]uint32{t, r.name}] = off
			off += dataEntrySize
		}
	}
	blobOff := map[[2]uint32]int{}
	for _, t := range types {
		for _, r := range byType[t] {
			for off%4 != 0 {
				off++
			}
			blobOff[[2]uint32{t, r.name}] = off
			off += len(r.data)
		}
	}
	buf := make([]byte, off)

	putDir := func(base int, entries [][2]uint32, targets []int32) {
		// entries: {name/id, target}; target high bit => subdirectory
		binary.LittleEndian.PutUint32(buf[base:], 0) // characteristics
		binary.LittleEndian.PutUint32(buf[base+4:], 0)
		binary.LittleEndian.PutUint32(buf[base+8:], 0)
		binary.LittleEndian.PutUint32(buf[base+12:], uint32(len(entries)))
		for i, e := range entries {
			p := base + 16 + i*8
			binary.LittleEndian.PutUint32(buf[p:], e[0])
			binary.LittleEndian.PutUint32(buf[p+4:], uint32(targets[i]))
		}
	}

	rootEntries := make([][2]uint32, 0, len(types))
	rootTargets := make([]int32, 0, len(types))
	for _, t := range types {
		rootEntries = append(rootEntries, [2]uint32{t, 0})
		rootTargets = append(rootTargets, int32(typeDirOff[t])|int32(-0x80000000))
	}
	putDir(0, rootEntries, rootTargets)

	for _, t := range types {
		entries := make([][2]uint32, 0, len(byType[t]))
		targets := make([]int32, 0, len(byType[t]))
		for _, r := range byType[t] {
			entries = append(entries, [2]uint32{r.name, 0})
			targets = append(targets, int32(nameDirOff[[2]uint32{t, r.name}])|int32(-0x80000000))
		}
		putDir(typeDirOff[t], entries, targets)
		for _, r := range byType[t] {
			key := [2]uint32{t, r.name}
			putDir(nameDirOff[key], [][2]uint32{{r.name, 0}}, []int32{int32(dataEntryOff[key])})
			de := dataEntryOff[key]
			binary.LittleEndian.PutUint32(buf[de:], uint32(blobOff[key]))
			binary.LittleEndian.PutUint32(buf[de+4:], uint32(len(r.data)))
			binary.LittleEndian.PutUint32(buf[de+8:], 0)
			binary.LittleEndian.PutUint32(buf[de+12:], 0)
			copy(buf[blobOff[key]:], r.data)
		}
	}
	return buf
}

func sortUint32(v []uint32) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="AllTag.TagEditor" version="1.0.0.0" processorArchitecture="amd64"/>
  <description>AllTag — редактор аудиотегов</description>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0"
        processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">permonitorv2</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
      <activeCodePage xmlns="http://schemas.microsoft.com/SMI/2019/WindowsSettings">UTF-8</activeCodePage>
    </windowsSettings>
  </application>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
      <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
      <supportedOS Id="{4f476546-9373-4e4f-9d9c-3d9bb0f6a0e6}"/>
    </application>
  </compatibility>
</assembly>
`

// writeSyso emits a COFF object with a single .rsrc section.
func writeSyso(path string, section []byte) error {
	symOff := 20 + 40 // no symbols, so data starts right after the headers
	for symOff%4 != 0 {
		symOff++
	}
	var buf bytes.Buffer
	// COFF file header
	binary.Write(&buf, binary.LittleEndian, uint16(0x8664)) // IMAGE_FILE_MACHINE_AMD64
	binary.Write(&buf, binary.LittleEndian, uint16(1))      // NumberOfSections
	binary.Write(&buf, binary.LittleEndian, uint32(0))      // TimeDateStamp
	binary.Write(&buf, binary.LittleEndian, uint32(0))      // PointerToSymbolTable
	binary.Write(&buf, binary.LittleEndian, uint32(0))      // NumberOfSymbols
	binary.Write(&buf, binary.LittleEndian, uint16(0))      // SizeOfOptionalHeader
	binary.Write(&buf, binary.LittleEndian, uint16(0))      // Characteristics
	// section header
	name := [8]byte{}
	copy(name[:], ".rsrc")
	buf.Write(name[:])
	binary.Write(&buf, binary.LittleEndian, uint32(len(section))) // VirtualSize
	binary.Write(&buf, binary.LittleEndian, uint32(0))            // VirtualAddress
	binary.Write(&buf, binary.LittleEndian, uint32(len(section))) // SizeOfRawData
	binary.Write(&buf, binary.LittleEndian, uint32(symOff))       // PointerToRawData
	binary.Write(&buf, binary.LittleEndian, uint32(0))            // PointerToRelocations
	binary.Write(&buf, binary.LittleEndian, uint32(0))            // PointerToLinenumbers
	binary.Write(&buf, binary.LittleEndian, uint16(0))            // NumberOfRelocations
	binary.Write(&buf, binary.LittleEndian, uint16(0))            // NumberOfLinenumbers
	// IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ
	binary.Write(&buf, binary.LittleEndian, uint32(0x40000040))
	for buf.Len() < symOff {
		buf.WriteByte(0)
	}
	buf.Write(section)
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	icons := buildIcons([]int{16, 20, 24, 32, 40, 48, 64, 128, 256})
	if len(os.Args) > 2 && os.Args[2] == "preview" {
		c := render(256)
		if err := os.WriteFile(filepath.Join(root, "icon-preview.png"), encodePNGIcon(c), 0o644); err != nil {
			fatal(err)
		}
	}
	if err := writeICO(filepath.Join(root, "assets", "icon.ico"), icons); err != nil {
		fatal(err)
	}

	var res []resource
	ids := make([]int, len(icons))
	for i, ic := range icons {
		ids[i] = i + 1
		res = append(res, resource{typ: rtIcon, name: uint32(i + 1), data: ic.data})
	}
	res = append(res, resource{typ: rtGroupIcon, name: 1, data: groupIcon(icons, ids)})
	res = append(res, resource{typ: rtManifest, name: 1, data: []byte(manifest)})

	section := buildResourceSection(res)
	out := filepath.Join(root, "rsrc_windows_amd64.syso")
	if err := writeSyso(out, section); err != nil {
		fatal(err)
	}
	fmt.Printf("icon: %d sizes, resources: %d bytes, syso: %s\n",
		len(icons), len(section), out)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "iconbuild:", err)
	os.Exit(1)
}
