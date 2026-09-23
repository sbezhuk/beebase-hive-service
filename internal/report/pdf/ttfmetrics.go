package pdf

import (
	"encoding/binary"
	"fmt"
)

// ttfMetrics contains the small subset of TrueType metrics needed to align
// outer report text by its visible ink rather than by the font origin.
type ttfMetrics struct {
	unitsPerEm int
	cmap       ttfCmap4
	glyphXMin  []int16
}

type ttfCmap4 struct {
	endCodes      []uint16
	startCodes    []uint16
	idDeltas      []int16
	idRangeOffset []uint16
	base          int
	data          []byte
}

func parseTTFMetrics(data []byte) (ttfMetrics, error) {
	tables, err := ttfTables(data)
	if err != nil {
		return ttfMetrics{}, err
	}
	head, err := ttfTable(data, tables, "head")
	if err != nil || len(head) < 52 {
		return ttfMetrics{}, fmt.Errorf("invalid head table")
	}
	unitsPerEm := int(binary.BigEndian.Uint16(head[18:20]))
	if unitsPerEm == 0 {
		return ttfMetrics{}, fmt.Errorf("font has no units per em")
	}
	maxp, err := ttfTable(data, tables, "maxp")
	if err != nil || len(maxp) < 6 {
		return ttfMetrics{}, fmt.Errorf("invalid maxp table")
	}
	numGlyphs := int(binary.BigEndian.Uint16(maxp[4:6]))
	loca, err := ttfTable(data, tables, "loca")
	if err != nil {
		return ttfMetrics{}, fmt.Errorf("invalid loca table: %w", err)
	}
	glyf, err := ttfTable(data, tables, "glyf")
	if err != nil {
		return ttfMetrics{}, fmt.Errorf("invalid glyf table: %w", err)
	}
	indexLocFormat := int16(binary.BigEndian.Uint16(head[50:52]))
	glyphXMin := make([]int16, numGlyphs)
	for glyph := 0; glyph < numGlyphs; glyph++ {
		start, end, ok := ttfGlyphRange(loca, glyph, indexLocFormat)
		if !ok || start >= end || end > len(glyf) || end-start < 6 {
			continue
		}
		glyphXMin[glyph] = int16(binary.BigEndian.Uint16(glyf[start+2 : start+4]))
	}
	cmap, err := ttfCmap(data, tables)
	if err != nil {
		return ttfMetrics{}, err
	}
	return ttfMetrics{unitsPerEm: unitsPerEm, cmap: cmap, glyphXMin: glyphXMin}, nil
}

func (m ttfMetrics) leftInkOffset(text string, unitSize float64) float64 {
	for _, r := range text {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			continue
		}
		glyph := m.cmap.glyphIndex(r)
		if glyph >= 0 && glyph < len(m.glyphXMin) {
			return float64(m.glyphXMin[glyph]) * unitSize / float64(m.unitsPerEm)
		}
		return 0
	}
	return 0
}

func ttfTables(data []byte) (map[string][]byte, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("font is too short")
	}
	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	if 12+16*numTables > len(data) {
		return nil, fmt.Errorf("font table directory is truncated")
	}
	tables := make(map[string][]byte, numTables)
	for i := 0; i < numTables; i++ {
		off := 12 + i*16
		name := string(data[off : off+4])
		start := int(binary.BigEndian.Uint32(data[off+8 : off+12]))
		length := int(binary.BigEndian.Uint32(data[off+12 : off+16]))
		if start < 0 || length < 0 || start > len(data) || length > len(data)-start {
			return nil, fmt.Errorf("font table %q is truncated", name)
		}
		tables[name] = data[start : start+length]
	}
	return tables, nil
}

func ttfTable(data []byte, tables map[string][]byte, name string) ([]byte, error) {
	table, ok := tables[name]
	if !ok {
		return nil, fmt.Errorf("missing %s table", name)
	}
	return table, nil
}

func ttfGlyphRange(loca []byte, glyph int, format int16) (start, end int, ok bool) {
	if glyph < 0 {
		return 0, 0, false
	}
	if format == 0 {
		base := glyph * 2
		if base+4 > len(loca) {
			return 0, 0, false
		}
		return int(binary.BigEndian.Uint16(loca[base:base+2])) * 2,
			int(binary.BigEndian.Uint16(loca[base+2:base+4])) * 2, true
	}
	base := glyph * 4
	if base+8 > len(loca) {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint32(loca[base : base+4])),
		int(binary.BigEndian.Uint32(loca[base+4 : base+8])), true
}

func ttfCmap(data []byte, tables map[string][]byte) (ttfCmap4, error) {
	table, err := ttfTable(data, tables, "cmap")
	if err != nil || len(table) < 4 {
		return ttfCmap4{}, fmt.Errorf("invalid cmap table")
	}
	numTables := int(binary.BigEndian.Uint16(table[2:4]))
	var selected []byte
	for i := 0; i < numTables; i++ {
		off := 4 + i*8
		if off+8 > len(table) {
			break
		}
		platform := binary.BigEndian.Uint16(table[off : off+2])
		encoding := binary.BigEndian.Uint16(table[off+2 : off+4])
		subOffset := int(binary.BigEndian.Uint32(table[off+4 : off+8]))
		if subOffset < 0 || subOffset+2 > len(table) || binary.BigEndian.Uint16(table[subOffset:subOffset+2]) != 4 {
			continue
		}
		if platform == 3 && (encoding == 1 || encoding == 10) || platform == 0 {
			selected = table[subOffset:]
			break
		}
	}
	if len(selected) < 16 {
		return ttfCmap4{}, fmt.Errorf("font has no supported unicode cmap")
	}
	segCount := int(binary.BigEndian.Uint16(selected[6:8]) / 2)
	endBase := 14
	startBase := endBase + segCount*2 + 2
	deltaBase := startBase + segCount*2
	rangeBase := deltaBase + segCount*2
	if rangeBase+segCount*2 > len(selected) {
		return ttfCmap4{}, fmt.Errorf("cmap format 4 is truncated")
	}
	endCodes := make([]uint16, segCount)
	startCodes := make([]uint16, segCount)
	idDeltas := make([]int16, segCount)
	idRangeOffset := make([]uint16, segCount)
	for i := 0; i < segCount; i++ {
		endCodes[i] = binary.BigEndian.Uint16(selected[endBase+i*2 : endBase+i*2+2])
		startCodes[i] = binary.BigEndian.Uint16(selected[startBase+i*2 : startBase+i*2+2])
		idDeltas[i] = int16(binary.BigEndian.Uint16(selected[deltaBase+i*2 : deltaBase+i*2+2]))
		idRangeOffset[i] = binary.BigEndian.Uint16(selected[rangeBase+i*2 : rangeBase+i*2+2])
	}
	return ttfCmap4{endCodes, startCodes, idDeltas, idRangeOffset, rangeBase, selected}, nil
}

func (c ttfCmap4) glyphIndex(r rune) int {
	if r < 0 || r > 0xffff {
		return 0
	}
	cp := uint16(r)
	for i, end := range c.endCodes {
		if cp > end || cp < c.startCodes[i] {
			continue
		}
		if c.idRangeOffset[i] == 0 {
			return int(uint16(int32(cp) + int32(c.idDeltas[i])))
		}
		// idRangeOffset is addressed from its own word in the cmap table.
		address := c.base + i*2 + int(c.idRangeOffset[i]) + int(cp-c.startCodes[i])*2
		if address+2 > len(c.data) {
			return 0
		}
		glyph := binary.BigEndian.Uint16(c.data[address : address+2])
		if glyph == 0 {
			return 0
		}
		return int(uint16(int32(glyph) + int32(c.idDeltas[i])))
	}
	return 0
}
