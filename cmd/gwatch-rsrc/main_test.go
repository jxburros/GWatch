package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// fakeICO builds a minimal but structurally real .ico so the tests do not
// depend on the committed artwork.
func fakeICO(t *testing.T, images [][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, uint16(0))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(len(images)))
	off := 6 + len(images)*16
	for i, img := range images {
		binary.Write(&b, binary.LittleEndian, iconDirEntry{
			Width: uint8(16 * (i + 1)), Height: uint8(16 * (i + 1)),
			Planes: 1, BitCount: 32,
			BytesInRes: uint32(len(img)), ImageOfs: uint32(off),
		})
		off += len(img)
	}
	for _, img := range images {
		b.Write(img)
	}
	return b.Bytes()
}

func TestParseICORejectsRubbish(t *testing.T) {
	good := fakeICO(t, [][]byte{{1, 2, 3}})
	for name, raw := range map[string][]byte{
		"empty":          {},
		"short":          {0, 0, 1},
		"not an icon":    {0, 0, 2, 0, 1, 0},
		"no images":      {0, 0, 1, 0, 0, 0},
		"truncated dir":  good[:10],
		"image past end": append(append([]byte{}, good[:6+12]...), []byte{0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0}...),
	} {
		if _, err := parseICO(raw); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestParseICORoundTrip(t *testing.T) {
	images := [][]byte{bytes.Repeat([]byte{0xaa}, 40), bytes.Repeat([]byte{0xbb}, 77)}
	icons, err := parseICO(fakeICO(t, images))
	if err != nil {
		t.Fatal(err)
	}
	if len(icons) != 2 {
		t.Fatalf("got %d icons, want 2", len(icons))
	}
	for i, ic := range icons {
		if !bytes.Equal(ic.data, images[i]) {
			t.Errorf("image %d: data does not match the source", i)
		}
		if int(ic.entry.BytesInRes) != len(images[i]) {
			t.Errorf("image %d: BytesInRes %d, want %d", i, ic.entry.BytesInRes, len(images[i]))
		}
	}
}

// TestGroupIconIsFourteenBytesPerEntry guards the one field-layout trap here:
// a GRPICONDIRENTRY is 14 bytes, not the 16 of the ICONDIRENTRY it mirrors, so
// anything that pads the struct produces a group Windows cannot read.
func TestGroupIconIsFourteenBytesPerEntry(t *testing.T) {
	icons, err := parseICO(fakeICO(t, [][]byte{{1}, {2}, {3}}))
	if err != nil {
		t.Fatal(err)
	}
	group := buildGroup(icons)
	if want := 6 + 3*14; len(group) != want {
		t.Fatalf("group is %d bytes, want %d", len(group), want)
	}
	if got := binary.LittleEndian.Uint16(group[4:]); got != 3 {
		t.Fatalf("group declares %d images, want 3", got)
	}
	for i := range icons {
		if id := binary.LittleEndian.Uint16(group[6+i*14+12:]); id != uint16(i+1) {
			t.Errorf("entry %d points at RT_ICON %d, want %d", i, id, i+1)
		}
	}
}

// walkRsrc resolves the resource tree the way a PE loader does, returning each
// leaf's section-relative data offset and size.
func walkRsrc(t *testing.T, sec []byte, off int, path []uint32, out map[[3]uint32][2]int) {
	t.Helper()
	named := binary.LittleEndian.Uint16(sec[off+12:])
	ids := binary.LittleEndian.Uint16(sec[off+14:])
	if named != 0 {
		t.Fatalf("named entries are never emitted, found %d", named)
	}
	var prev uint32
	for i := range int(ids) {
		e := off + 16 + i*8
		id := binary.LittleEndian.Uint32(sec[e:])
		child := binary.LittleEndian.Uint32(sec[e+4:])
		// The loader binary-searches these, so order is load-bearing.
		if i > 0 && id <= prev {
			t.Fatalf("entries out of order at depth %d: %d after %d", len(path), id, prev)
		}
		prev = id
		p := append(append([]uint32{}, path...), id)
		if child&0x80000000 != 0 {
			walkRsrc(t, sec, int(child&0x7fffffff), p, out)
			continue
		}
		if len(p) != 3 {
			t.Fatalf("leaf at depth %d, want 3", len(p))
		}
		dataOff := binary.LittleEndian.Uint32(sec[child:])
		size := binary.LittleEndian.Uint32(sec[child+4:])
		out[[3]uint32{p[0], p[1], p[2]}] = [2]int{int(dataOff), int(size)}
	}
}

// TestCOFFResolvesBackToTheSourceImages is the real test: build the object,
// then read the resource tree back out of its .rsrc section and confirm every
// leaf points at the bytes it is supposed to. This is what a linker does, and
// it is where an off-by-one in the offset arithmetic shows up.
func TestCOFFResolvesBackToTheSourceImages(t *testing.T) {
	images := [][]byte{
		bytes.Repeat([]byte{0x11}, 100),
		bytes.Repeat([]byte{0x22}, 333), // odd length, to exercise alignment
		bytes.Repeat([]byte{0x33}, 7),
	}
	icons, err := parseICO(fakeICO(t, images))
	if err != nil {
		t.Fatal(err)
	}
	res := make([]resource, 0, len(icons)+1)
	for i, ic := range icons {
		res = append(res, resource{typ: rtIcon, id: uint16(i + 1), data: ic.data})
	}
	group := buildGroup(icons)
	res = append(res, resource{typ: rtGroupIcon, id: groupIconID, data: group})

	for _, tgt := range targets {
		obj := buildCOFF(tgt, res)

		if got := binary.LittleEndian.Uint16(obj[0:]); got != tgt.machine {
			t.Errorf("%s: machine 0x%04x, want 0x%04x", tgt.goarch, got, tgt.machine)
		}
		if got := binary.LittleEndian.Uint16(obj[2:]); got != 1 {
			t.Fatalf("%s: %d sections, want 1", tgt.goarch, got)
		}
		if name := string(bytes.TrimRight(obj[20:28], "\x00")); name != ".rsrc" {
			t.Fatalf("%s: section named %q, want .rsrc", tgt.goarch, name)
		}
		secSize := int(binary.LittleEndian.Uint32(obj[36:]))
		secPtr := int(binary.LittleEndian.Uint32(obj[40:]))
		relPtr := int(binary.LittleEndian.Uint32(obj[44:]))
		nrel := int(binary.LittleEndian.Uint16(obj[52:]))
		sec := obj[secPtr : secPtr+secSize]

		if nrel != len(res) {
			t.Errorf("%s: %d relocations, want %d", tgt.goarch, nrel, len(res))
		}
		nsym := int(binary.LittleEndian.Uint32(obj[12:]))
		if nsym != len(res) {
			t.Errorf("%s: %d symbols, want %d", tgt.goarch, nsym, len(res))
		}

		leaves := map[[3]uint32][2]int{}
		walkRsrc(t, sec, 0, nil, leaves)
		if len(leaves) != len(res) {
			t.Fatalf("%s: %d leaves, want %d", tgt.goarch, len(leaves), len(res))
		}
		for _, r := range res {
			key := [3]uint32{uint32(r.typ), uint32(r.id), langEnglishUS}
			got, ok := leaves[key]
			if !ok {
				t.Fatalf("%s: no leaf for type %d id %d", tgt.goarch, r.typ, r.id)
			}
			off, size := got[0], got[1]
			if size != len(r.data) {
				t.Errorf("%s: type %d id %d size %d, want %d", tgt.goarch, r.typ, r.id, size, len(r.data))
			}
			if !bytes.Equal(sec[off:off+size], r.data) {
				t.Errorf("%s: type %d id %d resolves to the wrong bytes", tgt.goarch, r.typ, r.id)
			}
		}

		// Every relocation must target a data entry's OffsetToData field, and
		// that field must already hold the blob's section offset: the linker
		// only adds the section's virtual address to it.
		for i := range nrel {
			rec := obj[relPtr+i*10:]
			at := int(binary.LittleEndian.Uint32(rec))
			if typ := binary.LittleEndian.Uint16(rec[8:]); typ != tgt.relType {
				t.Errorf("%s: relocation %d type 0x%x, want 0x%x", tgt.goarch, i, typ, tgt.relType)
			}
			blobOff := int(binary.LittleEndian.Uint32(sec[at:]))
			size := int(binary.LittleEndian.Uint32(sec[at+4:]))
			if blobOff+size > len(sec) {
				t.Fatalf("%s: relocation %d points outside the section", tgt.goarch, i)
			}
		}
	}
}
