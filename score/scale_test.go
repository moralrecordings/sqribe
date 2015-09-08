package score

import (
	"fmt"
	"testing"

	"sqweek.net/sqribe/midi"
)

var origin map[KeySig]uint8

func init() {
	origin = map[KeySig]uint8{
		-7: midi.PitchC5-1, //Cb
		-6: midi.PitchC5+6, //Gb
		-5: midi.PitchC5+1, //Db
		-4: midi.PitchC5-4, //Ab
		-3: midi.PitchC5+3, //Eb
		-2: midi.PitchC5-2, //Bb
		-1: midi.PitchC5+5, //F
		0: midi.PitchC5,
		1: midi.PitchC5+7, //G
		2: midi.PitchC5+2, //D
		3: midi.PitchA4,
		4: midi.PitchC5+4, //E
		5: midi.PitchC5-1, //B
		6: midi.PitchC5+6, //F#
		7: midi.PitchC5+1, //C#
	}
}

func axstr(ax int) string {
	switch ax {
	case -2: return "♭♭"
	case -1: return "♭"
	case 0: return "♮"
	case 1: return "♯"
	case 2: return "x"
	default: return fmt.Sprint(ax)
	}
}

func testClefLines(t *testing.T, clef *Clef, key KeySig) {
	fail := false
	lines := make([]struct{uint8; int; string}, 0)
	for _, d := range scale2degree {
		pitch := origin[key] + uint8(d)
		line, ax := clef.LineForPitch(key, pitch)
		axs := ""
		if ax != nil {
			axs = axstr(*ax)
		}
		for _, prev := range lines {
			if prev.int == line {
				fail = true // same line used twice within scale
			}
		}
		lines = append(lines, struct{uint8; int; string}{pitch, line, axs})
	}
	if fail {
		t.Errorf("%s %v:\n", clef.Name, key)
		for _, res := range lines {
			t.Errorf("	%4s %3d %s\n", midi.PitchName(res.uint8), res.int, res.string)
		}
	}
}

func TestScaleLines(t *testing.T) {
	for _, clef := range []*Clef{&TrebleClef, &BassClef} {
		for key := KeySig(-7); key <= 7; key++ {
			testClefLines(t, clef, key)
		}
	}
}