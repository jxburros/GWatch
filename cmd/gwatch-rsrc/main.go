// Command gwatch-rsrc turns a Windows .ico into the .syso resource objects the
// Go linker embeds into gwatch.exe and gwatch-agent.exe.
//
// A Go binary has no icon of its own: Explorer, the task bar and the Alt-Tab
// list all fall back to the generic Windows program icon unless a COFF object
// carrying an RT_GROUP_ICON resource is linked in. The linker picks up any
// *.syso in a package directory and merges its .rsrc section into the PE it
// produces, which is what this command writes.
//
//	go run ./cmd/gwatch-rsrc -ico scripts/installer/assets/gwatch.ico -out rsrc
//
// That writes rsrc_windows_amd64.syso and rsrc_windows_arm64.syso. The GOOS and
// GOARCH suffixes matter: they are ordinary Go build constraints, and without
// them the object would be handed to the linker on Linux and macOS too, where
// it is meaningless and breaks a cgo build.
//
// Deliberately icon-only. Version information belongs in a PE as well, but the
// version is a build-time value (-ldflags "-X main.version=..."), and baking it
// into a committed object would mean every release shipping a binary whose
// properties disagree with itself.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// Resource types, from the Windows SDK. Only these two are used: each image in
// the .ico becomes an RT_ICON, and one RT_GROUP_ICON ties them together into
// the thing Windows actually looks up when it wants "the icon for this file".
const (
	rtIcon      = 3
	rtGroupIcon = 14
)

// langEnglishUS is the language every resource is filed under. A neutral
// language would do as well; this matches what the usual Windows resource
// compilers emit, so the tree looks unremarkable to anything reading it.
const langEnglishUS = 0x0409

// groupIconID is the RT_GROUP_ICON identifier. Windows uses the numerically
// lowest one in the binary as the application icon, so there is exactly one and
// it is 1.
const groupIconID = 1

// iconDirEntry is one image's row in the .ico file's directory.
type iconDirEntry struct {
	Width      uint8
	Height     uint8
	ColorCount uint8
	Reserved   uint8
	Planes     uint16
	BitCount   uint16
	BytesInRes uint32
	ImageOfs   uint32
}

// grpIconDirEntry is the same row as it appears inside an RT_GROUP_ICON: the
// trailing file offset is replaced by the resource id of the RT_ICON holding
// that image. It is 14 bytes on disk, not 16, so it cannot be written by
// dumping the struct with padding.
type grpIconDirEntry struct {
	Width      uint8
	Height     uint8
	ColorCount uint8
	Reserved   uint8
	Planes     uint16
	BitCount   uint16
	BytesInRes uint32
	ID         uint16
}

type icon struct {
	entry iconDirEntry
	data  []byte
}

func parseICO(raw []byte) ([]icon, error) {
	if len(raw) < 6 {
		return nil, fmt.Errorf("too short to be an .ico (%d bytes)", len(raw))
	}
	reserved := binary.LittleEndian.Uint16(raw[0:])
	typ := binary.LittleEndian.Uint16(raw[2:])
	count := binary.LittleEndian.Uint16(raw[4:])
	if reserved != 0 || typ != 1 {
		return nil, fmt.Errorf("not an icon file (reserved=%d type=%d)", reserved, typ)
	}
	if count == 0 {
		return nil, fmt.Errorf("icon file contains no images")
	}
	if len(raw) < 6+int(count)*16 {
		return nil, fmt.Errorf("icon directory is truncated")
	}
	icons := make([]icon, 0, count)
	for i := range int(count) {
		b := raw[6+i*16:]
		e := iconDirEntry{
			Width:      b[0],
			Height:     b[1],
			ColorCount: b[2],
			Reserved:   b[3],
			Planes:     binary.LittleEndian.Uint16(b[4:]),
			BitCount:   binary.LittleEndian.Uint16(b[6:]),
			BytesInRes: binary.LittleEndian.Uint32(b[8:]),
			ImageOfs:   binary.LittleEndian.Uint32(b[12:]),
		}
		end := uint64(e.ImageOfs) + uint64(e.BytesInRes)
		if end > uint64(len(raw)) {
			return nil, fmt.Errorf("image %d runs past the end of the file", i)
		}
		icons = append(icons, icon{entry: e, data: raw[e.ImageOfs:end]})
	}
	return icons, nil
}

// buildGroup renders the RT_GROUP_ICON blob: a three-field header followed by
// one 14-byte row per image, in the same order as the icons themselves, whose
// resource ids run 1..len(icons).
func buildGroup(icons []icon) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, uint16(0))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(len(icons)))
	for i, ic := range icons {
		binary.Write(&b, binary.LittleEndian, grpIconDirEntry{
			Width:      ic.entry.Width,
			Height:     ic.entry.Height,
			ColorCount: ic.entry.ColorCount,
			Reserved:   ic.entry.Reserved,
			Planes:     ic.entry.Planes,
			BitCount:   ic.entry.BitCount,
			BytesInRes: ic.entry.BytesInRes,
			ID:         uint16(i + 1),
		})
	}
	return b.Bytes()
}

// resource is one leaf of the tree: a type, an id within that type, and bytes.
type resource struct {
	typ  uint16
	id   uint16
	data []byte
}

const (
	dirSize       = 16 // IMAGE_RESOURCE_DIRECTORY
	dirEntrySize  = 8  // IMAGE_RESOURCE_DIRECTORY_ENTRY
	dataEntrySize = 16 // IMAGE_RESOURCE_DATA_ENTRY
	dataAlign     = 8
)

func align(n, to int) int { return (n + to - 1) / to * to }

// buildRsrc lays out the .rsrc section and returns it along with the offset of
// every IMAGE_RESOURCE_DATA_ENTRY and of the blob it points at.
//
// The tree is three levels deep — type, then id, then language — which is what
// Windows expects. Entries at each level must be sorted by id ascending,
// because the loader binary-searches them.
//
// A data entry's OffsetToData is an RVA, and an object file has no addresses
// yet, so a relocation is emitted for each one. PE relocations carry their
// addend in the field itself rather than alongside it, so the field is written
// with the blob's offset from the start of this section and the linker adds the
// section's virtual address to it. Both linkers that matter agree on that
// reading: Go's internal one takes the field as the addend directly, and an
// external COFF linker computes RVA(section symbol) + addend, which comes to
// the same thing because the symbol sits at offset zero.
func buildRsrc(res []resource) (section []byte, dataEntryOfs []int) {
	types := []uint16{}
	byType := map[uint16][]int{}
	for i, r := range res {
		if _, seen := byType[r.typ]; !seen {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], i)
	}

	// Pass one: sizes, so every offset is known before anything is written.
	off := dirSize + len(types)*dirEntrySize
	typeDirOfs := map[uint16]int{}
	for _, t := range types {
		typeDirOfs[t] = off
		off += dirSize + len(byType[t])*dirEntrySize
	}
	nameDirOfs := make([]int, len(res))
	for _, t := range types {
		for _, i := range byType[t] {
			nameDirOfs[i] = off
			off += dirSize + dirEntrySize // exactly one language
		}
	}
	dataEntryOfs = make([]int, len(res))
	for i := range res {
		dataEntryOfs[i] = off
		off += dataEntrySize
	}
	blobOfs := make([]int, len(res))
	for i, r := range res {
		off = align(off, dataAlign)
		blobOfs[i] = off
		off += len(r.data)
	}

	buf := make([]byte, align(off, dataAlign))
	putDir := func(at, named, ids int) {
		binary.LittleEndian.PutUint16(buf[at+12:], uint16(named))
		binary.LittleEndian.PutUint16(buf[at+14:], uint16(ids))
	}
	// The high bit of OffsetToData marks a subdirectory rather than a leaf.
	putEntry := func(at int, id uint16, target int, isDir bool) {
		binary.LittleEndian.PutUint32(buf[at:], uint32(id))
		v := uint32(target)
		if isDir {
			v |= 0x80000000
		}
		binary.LittleEndian.PutUint32(buf[at+4:], v)
	}

	putDir(0, 0, len(types))
	at := dirSize
	for _, t := range types {
		putEntry(at, t, typeDirOfs[t], true)
		at += dirEntrySize
	}
	for _, t := range types {
		putDir(typeDirOfs[t], 0, len(byType[t]))
		at = typeDirOfs[t] + dirSize
		for _, i := range byType[t] {
			putEntry(at, res[i].id, nameDirOfs[i], true)
			at += dirEntrySize
		}
	}
	for i, r := range res {
		putDir(nameDirOfs[i], 0, 1)
		putEntry(nameDirOfs[i]+dirSize, langEnglishUS, dataEntryOfs[i], false)
		binary.LittleEndian.PutUint32(buf[dataEntryOfs[i]:], uint32(blobOfs[i]))
		binary.LittleEndian.PutUint32(buf[dataEntryOfs[i]+4:], uint32(len(r.data)))
		copy(buf[blobOfs[i]:], r.data)
	}
	return buf, dataEntryOfs
}

type target struct {
	goarch  string
	machine uint16
	relType uint16 // the architecture's "32-bit RVA" relocation
}

// IMAGE_REL_*_ADDR32NB: write the target's address as an RVA, i.e. relative to
// the image base. Same meaning on both architectures, different numbers.
var targets = []target{
	{"amd64", 0x8664, 0x0003},
	{"arm64", 0xAA64, 0x0002},
}

func buildCOFF(t target, res []resource) []byte {
	section, dataEntryOfs := buildRsrc(res)

	const (
		fileHeaderSize = 20
		sectHeaderSize = 40
		relocSize      = 10
		symbolSize     = 18
	)
	sectionOfs := fileHeaderSize + sectHeaderSize
	relocOfs := sectionOfs + len(section)
	symbolOfs := relocOfs + len(res)*relocSize

	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }

	w(t.machine)
	w(uint16(1)) // one section: .rsrc
	w(uint32(0)) // no timestamp, so the output is reproducible
	w(uint32(symbolOfs))
	w(uint32(len(res))) // one symbol per relocation
	w(uint16(0))        // no optional header in an object file
	w(uint16(0))

	b.Write([]byte(".rsrc\x00\x00\x00"))
	w(uint32(0)) // VirtualSize: unused in an object
	w(uint32(0)) // VirtualAddress: assigned by the linker
	w(uint32(len(section)))
	w(uint32(sectionOfs))
	w(uint32(relocOfs))
	w(uint32(0)) // no line numbers
	w(uint16(len(res)))
	w(uint16(0))
	w(uint32(0x40000040)) // CNT_INITIALIZED_DATA | MEM_READ

	b.Write(section)

	for i := range res {
		w(uint32(dataEntryOfs[i])) // the OffsetToData field to fix up
		w(uint32(i))               // ...using symbol i
		w(t.relType)
	}
	for i := range res {
		// Eight characters exactly, so the name fits inline and no string
		// table entry is needed.
		fmt.Fprintf(&b, "$R%06d", i)
		w(uint32(0)) // value: offset zero, so the addend in the field is the
		w(uint16(1)) // whole story; section number: .rsrc is section 1
		w(uint16(0)) // type: not a function
		w(uint8(3))  // storage class: static
		w(uint8(0))  // no auxiliary records
	}
	w(uint32(4)) // empty string table, which is still four bytes of length

	return b.Bytes()
}

func main() {
	ico := flag.String("ico", "scripts/installer/assets/gwatch.ico", "icon file to embed")
	out := flag.String("out", "rsrc", "output path prefix; _windows_<arch>.syso is appended")
	flag.Parse()

	raw, err := os.ReadFile(*ico)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gwatch-rsrc:", err)
		os.Exit(1)
	}
	icons, err := parseICO(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gwatch-rsrc: %s: %v\n", *ico, err)
		os.Exit(1)
	}

	res := make([]resource, 0, len(icons)+1)
	for i, ic := range icons {
		res = append(res, resource{typ: rtIcon, id: uint16(i + 1), data: ic.data})
	}
	res = append(res, resource{typ: rtGroupIcon, id: groupIconID, data: buildGroup(icons)})

	for _, t := range targets {
		path := fmt.Sprintf("%s_windows_%s.syso", *out, t.goarch)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "gwatch-rsrc:", err)
			os.Exit(1)
		}
		obj := buildCOFF(t, res)
		if err := os.WriteFile(path, obj, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "gwatch-rsrc:", err)
			os.Exit(1)
		}
		fmt.Printf("%s (%d images, %d bytes)\n", path, len(icons), len(obj))
	}
}
