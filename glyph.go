package main

import (
	"image/color"
	"image"
	"math"
)

type CenteredGlyph struct {
	col color.RGBA
	p image.Point //center
	r int //radius
}

func (g *CenteredGlyph) ColorModel() color.Model {
	return color.RGBAModel
}

func (g *CenteredGlyph) Bounds() image.Rectangle {
	return image.Rect(g.p.X - g.r, g.p.Y - g.r, g.p.X + g.r + 1, g.p.Y + g.r + 1)
}

type NoteHead struct {
	CenteredGlyph
	α float64
	hollowness float64
}

func (n *NoteHead) At(x, y int) color.Color {
	xx, yy, rr := float64(x - n.p.X)+0.5, float64(y - n.p.Y)+0.5, float64(n.r)
	rx := xx * math.Cos(n.α) - yy * math.Sin(n.α)
	ry := xx * math.Sin(n.α) + yy * math.Cos(n.α)
	rr2 := rr*rr
	dist2 := rx*rx + 1.25*1.25*ry*ry
	if dist2 < rr2 && dist2 >= n.hollowness * rr2 {
		return n.col
	}
	return color.RGBA{0, 0, 0, 0}
}

func newNoteHead(col color.RGBA, p image.Point, r int, α float64) *NoteHead {
	return &NoteHead{CenteredGlyph{col, p, r}, α, 0.0}
}

func newHollowNote(col color.RGBA, p image.Point, r int, α float64) *NoteHead {
	return &NoteHead{CenteredGlyph{col, p, r}, α, 0.6}
}

type NoteTail struct {
	CenteredGlyph
	downBeam bool
}

func (t *NoteTail) At(x, y int) color.Color {
	dx, dy := x - t.p.X, y - t.p.Y
	if dx > 0 && ((t.downBeam && dx + dy == 0) || (!t.downBeam && dx - dy == 0)) {
		return t.col
	}
	return color.RGBA{0, 0, 0, 0}
}

type FlatGlyph struct {
	CenteredGlyph
}

func (f *FlatGlyph) At(x, y int) color.Color {
	dx, dy := x - f.p.X, y - f.p.Y + 3
	if dx == -2 ||
	    (dy <= 5 && dy >= 3 && dy + dx == 4) ||
	    (dy < 3 && dy >= 1 && dy - dx == 2) {
		return f.col
	}
	return color.RGBA{0, 0, 0, 0}
}

// HACK the flat glyph is not aligned with the actual centre point 'p'
func (f *FlatGlyph) Bounds() image.Rectangle {
	return f.CenteredGlyph.Bounds().Sub(image.Point{0, 3})
}

type SharpGlyph struct {
	CenteredGlyph
}

func (s *SharpGlyph) At(x, y int) color.Color {
	dx, dy := s.p.X - x, s.p.Y - y
	line := dy + ceil(dx, 2)
	if (dx == -2 || dx == 2) ||
	    (line == 2 || line == -2) {
		return s.col
	}
	return color.RGBA{0, 0, 0, 0}
}

type NaturalGlyph struct {
	CenteredGlyph
}

func (n *NaturalGlyph) At(x, y int) color.Color {
	dx, dy := x - n.p.X, y - n.p.Y
	line := dy + divØ(dx, 2)
	if (dx == -2 && dy < 3) ||
	    (dx == 2 && dy > -3) ||
	    (dx > -3 && dx < 3 && (line == 1 || line == -1)) {
		return n.col
	}
	return color.RGBA{0, 0, 0, 0}
}

type DefaultGlyph struct {
	CenteredGlyph
}

func (d *DefaultGlyph) At(x, y int) color.Color {
	inX := (x > d.p.X - 3 && x < d.p.X + 3)
	inY := (y > d.p.Y - 3 && y < d.p.Y + 3)
	if (x == d.p.X - 3 && inY) ||
	    (x == d.p.X + 3 && inY) ||
	    (y == d.p.Y - 3 && inX) ||
	    (y == d.p.Y + 3 && inX) {
		return d.col
	}
	return color.RGBA{0, 0, 0, 0}
}

func newAccidental(col color.RGBA, p image.Point, r int, accidental int) image.Image {
	switch accidental {
	case -1: return &FlatGlyph{CenteredGlyph{col, p, r}}
	case 0: return &NaturalGlyph{CenteredGlyph{col, p, r}}
	case 1: return &SharpGlyph{CenteredGlyph{col, p, r}}
	}
	return &DefaultGlyph{CenteredGlyph{col, p, r}}
}