package main

import (
	"bytes"
	"compress/zlib"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// The letter font, embedded in every letter PDF: Liberation Sans (SIL Open
// Font License, fonts/LICENSE-LiberationSans.txt) has the advance widths of
// Helvetica, so the layout (fontmetrics.go) stays exact. Without an
// embedded font each viewer and printer picks a stand-in of its own, often
// a thin or blurry one. Only the glyphs of WinAnsiEncoding go in, which
// keeps it to a few dozen kilobytes.

//go:embed fonts/LiberationSans-Regular.ttf fonts/LiberationSans-Bold.ttf
var fontFiles embed.FS

type pdfFont struct {
	data       []byte // the subset TrueType file
	name       string // e.g. SBXAAA+LiberationSans
	bbox       [4]int // in 1/1000 em
	ascent     int
	descent    int
	capHeight  int
	unitsPerEm int
}

var letterFonts struct {
	once        sync.Once
	regular     *pdfFont
	bold        *pdfFont
	err         error
	regularName string
}

// fontsForLetters: the subset fonts, made once.
func fontsForLetters() (*pdfFont, *pdfFont, error) {
	letterFonts.once.Do(func() {
		var chars []rune
		for b := 32; b < 256; b++ {
			if r, ok := winAnsiRune(byte(b)); ok {
				chars = append(chars, r)
			}
		}
		load := func(file, name string) *pdfFont {
			raw, err := fontFiles.ReadFile("fonts/" + file)
			if err != nil {
				letterFonts.err = err
				return nil
			}
			f, err := subsetTrueType(raw, chars)
			if err != nil {
				letterFonts.err = fmt.Errorf("%s: %w", file, err)
				return nil
			}
			f.name = name
			return f
		}
		letterFonts.regular = load("LiberationSans-Regular.ttf", "SBXAAA+LiberationSans")
		letterFonts.bold = load("LiberationSans-Bold.ttf", "SBXAAB+LiberationSans-Bold")
	})
	return letterFonts.regular, letterFonts.bold, letterFonts.err
}

// winAnsiRune is the character a WinAnsiEncoding byte stands for (the ones ansi() writes).
func winAnsiRune(b byte) (rune, bool) {
	if b >= 32 && b < 127 || b >= 0xA0 {
		return rune(b), true
	}
	for _, r := range "€‚„…‘’“”–—ŠšŽžœŒ" {
		if x, ok := ansi(r); ok && x == b {
			return r, true
		}
	}
	return 0, false
}

// pdfObjects: the font dictionary (to be referenced as /F1 or /F2), its
// descriptor and the font file, as PDF objects added with add.
func (f *pdfFont) pdfObjects(add func(string) int, widths *[256]uint16) int {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write(f.data)
	w.Close()
	file := add(fmt.Sprintf("<< /Length %d /Length1 %d /Filter /FlateDecode >>\nstream\n%s\nendstream", z.Len(), len(f.data), z.Bytes()))
	desc := add(fmt.Sprintf("<< /Type /FontDescriptor /FontName /%s /Flags 32 /FontBBox [%d %d %d %d] /ItalicAngle 0 /Ascent %d /Descent %d /CapHeight %d /StemV 80 /FontFile2 %d 0 R >>",
		f.name, f.bbox[0], f.bbox[1], f.bbox[2], f.bbox[3], f.ascent, f.descent, f.capHeight, file))
	var ws bytes.Buffer
	for c := 32; c < 256; c++ {
		fmt.Fprintf(&ws, "%d ", widths[c])
	}
	return add(fmt.Sprintf("<< /Type /Font /Subtype /TrueType /BaseFont /%s /FirstChar 32 /LastChar 255 /Widths [%s] /Encoding /WinAnsiEncoding /FontDescriptor %d 0 R >>",
		f.name, bytes.TrimSpace(ws.Bytes()), desc))
}

// subsetTrueType keeps the glyphs of the given characters (and the parts of
// composite glyphs); the others stay in place but empty, so glyph numbers,
// cmap and widths don't change. Tables a PDF viewer doesn't use for a simple
// font (layout, kerning) are left out.
func subsetTrueType(raw []byte, chars []rune) (*pdfFont, error) {
	if len(raw) < 12 {
		return nil, errors.New("not a TrueType font")
	}
	n := int(binary.BigEndian.Uint16(raw[4:]))
	tables := map[string][]byte{}
	for i := 0; i < n; i++ {
		e := 12 + 16*i
		if e+16 > len(raw) {
			return nil, errors.New("broken table directory")
		}
		tag := string(raw[e : e+4])
		off, ln := int(binary.BigEndian.Uint32(raw[e+8:])), int(binary.BigEndian.Uint32(raw[e+12:]))
		if off+ln > len(raw) {
			return nil, errors.New("broken table " + tag)
		}
		tables[tag] = raw[off : off+ln]
	}
	for _, t := range []string{"head", "hhea", "hmtx", "maxp", "loca", "glyf", "cmap"} {
		if tables[t] == nil {
			return nil, errors.New("no " + t + " table")
		}
	}
	head := tables["head"]
	upem := int(binary.BigEndian.Uint16(head[18:]))
	longLoca := binary.BigEndian.Uint16(head[50:]) == 1
	numGlyphs := int(binary.BigEndian.Uint16(tables["maxp"][4:]))
	loca := func(i int) int {
		if longLoca {
			return int(binary.BigEndian.Uint32(tables["loca"][4*i:]))
		}
		return 2 * int(binary.BigEndian.Uint16(tables["loca"][2*i:]))
	}
	if len(tables["loca"]) < (numGlyphs+1)*map[bool]int{true: 4, false: 2}[longLoca] {
		return nil, errors.New("loca too short")
	}
	cmap, err := unicodeCmap(tables["cmap"])
	if err != nil {
		return nil, err
	}

	keep := map[int]bool{0: true} // .notdef
	glyf := tables["glyf"]
	var visit func(g int)
	visit = func(g int) {
		if g < 0 || g >= numGlyphs || keep[g] && g != 0 {
			return
		}
		keep[g] = true
		a, b := loca(g), loca(g+1)
		if b-a < 10 || b > len(glyf) || int16(binary.BigEndian.Uint16(glyf[a:])) >= 0 {
			return // empty or a simple glyph
		}
		// composite: follow its components
		p := a + 10
		for p+4 <= b {
			flags := binary.BigEndian.Uint16(glyf[p:])
			visit(int(binary.BigEndian.Uint16(glyf[p+2:])))
			p += 4
			if flags&1 != 0 { // ARG_1_AND_2_ARE_WORDS
				p += 4
			} else {
				p += 2
			}
			switch {
			case flags&0x08 != 0: // WE_HAVE_A_SCALE
				p += 2
			case flags&0x40 != 0: // X_AND_Y_SCALE
				p += 4
			case flags&0x80 != 0: // TWO_BY_TWO
				p += 8
			}
			if flags&0x20 == 0 { // no MORE_COMPONENTS
				break
			}
		}
	}
	for _, r := range chars {
		if g, ok := cmap[r]; ok {
			visit(g)
		}
	}

	var newGlyf, newLoca bytes.Buffer
	for g := 0; g < numGlyphs; g++ {
		binary.Write(&newLoca, binary.BigEndian, uint32(newGlyf.Len()))
		if keep[g] {
			a, b := loca(g), loca(g+1)
			if a <= b && b <= len(glyf) {
				newGlyf.Write(glyf[a:b])
				for newGlyf.Len()%4 != 0 {
					newGlyf.WriteByte(0)
				}
			}
		}
	}
	binary.Write(&newLoca, binary.BigEndian, uint32(newGlyf.Len()))
	newHead := append([]byte(nil), head...)
	binary.BigEndian.PutUint16(newHead[50:], 1) // long loca
	binary.BigEndian.PutUint32(newHead[8:], 0)  // checksum adjustment, set below
	out := map[string][]byte{"head": newHead, "glyf": newGlyf.Bytes(), "loca": newLoca.Bytes()}
	for _, t := range []string{"hhea", "hmtx", "maxp", "cmap", "cvt ", "fpgm", "prep", "name", "OS/2", "post", "gasp"} {
		if tables[t] != nil {
			out[t] = tables[t]
		}
	}
	data := writeSfnt(out)
	sum := checksum(data)
	binary.BigEndian.PutUint32(data[headOffset(data):][8:], 0xB1B0AFBA-sum)

	f := &pdfFont{data: data, unitsPerEm: upem}
	scale := func(v int16) int { return int(v) * 1000 / upem }
	f.bbox = [4]int{scale(int16(binary.BigEndian.Uint16(head[36:]))), scale(int16(binary.BigEndian.Uint16(head[38:]))),
		scale(int16(binary.BigEndian.Uint16(head[40:]))), scale(int16(binary.BigEndian.Uint16(head[42:])))}
	hhea := tables["hhea"]
	f.ascent, f.descent = scale(int16(binary.BigEndian.Uint16(hhea[4:]))), scale(int16(binary.BigEndian.Uint16(hhea[6:])))
	f.capHeight = f.ascent
	if os2 := tables["OS/2"]; len(os2) >= 90 && binary.BigEndian.Uint16(os2) >= 2 {
		f.capHeight = scale(int16(binary.BigEndian.Uint16(os2[88:])))
	}
	return f, nil
}

// unicodeCmap reads the Windows Unicode (3,1) format 4 subtable.
func unicodeCmap(t []byte) (map[rune]int, error) {
	if len(t) < 4 {
		return nil, errors.New("broken cmap")
	}
	n := int(binary.BigEndian.Uint16(t[2:]))
	for i := 0; i < n; i++ {
		e := 4 + 8*i
		if e+8 > len(t) {
			break
		}
		pid, eid, off := binary.BigEndian.Uint16(t[e:]), binary.BigEndian.Uint16(t[e+2:]), int(binary.BigEndian.Uint32(t[e+4:]))
		if pid != 3 || eid != 1 || off+14 > len(t) || binary.BigEndian.Uint16(t[off:]) != 4 {
			continue
		}
		s := t[off:]
		segs := int(binary.BigEndian.Uint16(s[6:])) / 2
		if 16+8*segs > len(s) {
			return nil, errors.New("broken cmap format 4")
		}
		ends, starts := 14, 16+2*segs
		deltas, ranges := starts+2*segs, starts+4*segs
		m := map[rune]int{}
		for k := 0; k < segs; k++ {
			end := int(binary.BigEndian.Uint16(s[ends+2*k:]))
			start := int(binary.BigEndian.Uint16(s[starts+2*k:]))
			delta := int(binary.BigEndian.Uint16(s[deltas+2*k:]))
			ro := int(binary.BigEndian.Uint16(s[ranges+2*k:]))
			for c := start; c <= end && c != 0xFFFF; c++ {
				g := 0
				if ro == 0 {
					g = (c + delta) & 0xFFFF
				} else {
					p := ranges + 2*k + ro + 2*(c-start)
					if p+2 > len(s) {
						continue
					}
					if g = int(binary.BigEndian.Uint16(s[p:])); g != 0 {
						g = (g + delta) & 0xFFFF
					}
				}
				if g != 0 {
					m[rune(c)] = g
				}
			}
		}
		return m, nil
	}
	return nil, errors.New("no Unicode cmap")
}

// writeSfnt puts tables together into a TrueType file.
func writeSfnt(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	n := len(tags)
	es, sr := 0, 1
	for sr*2 <= n {
		sr *= 2
		es++
	}
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, []uint16{0x0001, 0x0000, uint16(n), uint16(sr * 16), uint16(es), uint16(n*16 - sr*16)})
	off := 12 + 16*n
	for _, t := range tags {
		d := tables[t]
		b.WriteString(t)
		binary.Write(&b, binary.BigEndian, []uint32{checksum(d), uint32(off), uint32(len(d))})
		off += (len(d) + 3) &^ 3
	}
	for _, t := range tags {
		b.Write(tables[t])
		for b.Len()%4 != 0 {
			b.WriteByte(0)
		}
	}
	return b.Bytes()
}

func checksum(d []byte) uint32 {
	var s uint32
	for i := 0; i < len(d); i += 4 {
		var w [4]byte
		copy(w[:], d[i:])
		s += binary.BigEndian.Uint32(w[:])
	}
	return s
}

// sfntTable is one table of a TrueType file.
func sfntTable(data []byte, tag string) []byte {
	n := int(binary.BigEndian.Uint16(data[4:]))
	for i := 0; i < n; i++ {
		e := 12 + 16*i
		if string(data[e:e+4]) == tag {
			off, ln := int(binary.BigEndian.Uint32(data[e+8:])), int(binary.BigEndian.Uint32(data[e+12:]))
			return data[off : off+ln]
		}
	}
	return nil
}

// headOffset: where the head table starts in a file made by writeSfnt.
func headOffset(data []byte) int {
	n := int(binary.BigEndian.Uint16(data[4:]))
	for i := 0; i < n; i++ {
		e := 12 + 16*i
		if string(data[e:e+4]) == "head" {
			return int(binary.BigEndian.Uint32(data[e+8:]))
		}
	}
	return 0
}
