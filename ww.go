package main

import (
	"image"
	"math/big"
	"time"
	"fmt"

	"github.com/skelterjohn/go.wde"

	"sqweek.net/sqribe/audio"
	"sqweek.net/sqribe/midi"
	"sqweek.net/sqribe/score"
	"sqweek.net/sqribe/wave"

	. "sqweek.net/sqribe/core/types"
)

type changeMask int

const (
	WAV changeMask = 1 << iota
	SELXN
	MIXER
	SCALE
	CURSOR
	BEATS
	VIEWPOS
	LAYOUT
	MAXBIT
	EVERYTHING changeMask = MAXBIT - 1
)

const yspacing = 12 // pixels between staff lines

type noteProspect struct {
	delta int
	beatf score.BeatPoint
	staff *score.Staff
}

func (n *noteProspect) Eq(n2 *noteProspect) bool {
	return n.staff == n2.staff && n.delta == n2.delta && n.beatf == n2.beatf
}

type noteDrag struct {
	Δpitch int8
	Δbeat *big.Rat
}

type mouseState struct {
	cursor wde.Cursor
	dragFn DragFn
	note *noteProspect
	ndelta *noteDrag
	rectSelect *image.Rectangle
}

type WaveWidget struct {
	WidgetCore

	/* data related state */
	wav *wave.Waveform
	score *score.Score
	iolisten <-chan *wave.Chunk

	/* view related state */
	first_frame FrameN
	frames_per_pixel int
	selection TimeRange
	rect WaveLayout
	notesel map[*score.Note]*score.Staff
	snarf map[*score.Staff] []*score.Note // the cut/copy buffer
	pasteMode bool

	/* renderer related state */
	renderstate struct {
		img *image.RGBA
		waveRulers *image.RGBA
		changed changeMask
		cursor *image.RGBA
		cursorPrevX int
	}
	mouse struct {
		pos image.Point
		state *mouseState
	}
	cursorX int
}

func NewWaveWidget(refresh chan Widget) *WaveWidget {
	var ww WaveWidget
	ww.first_frame = 0
	ww.frames_per_pixel = 512
	ww.rect.staves = make(map[*score.Staff]image.Rectangle)
	ww.rect.mixers = make(map[*score.Staff]*MixerLayout)
	ww.selection = &FrameRange{0, 0}
	ww.notesel = make(map[*score.Note]*score.Staff)
	ww.renderstate.img = nil
	ww.renderstate.changed = WAV
	ww.refresh = refresh
	return &ww
}

func (ww *WaveWidget) changed(mask changeMask, ev interface{}) {
	ww.renderstate.changed |= mask
	ww.refresh <- ww
}

func (ww *WaveWidget) SelectAudio(sel TimeRange) {
	ww.selection = sel
	G.plumb.selection.C <- sel
	ww.changed(SELXN, sel)
}

func (ww *WaveWidget) SelectAudioSnapToBeats(start, end FrameN) {
	sc := ww.score
	if sc == nil {
		ww.SelectAudio(FrameRange{start, end})
	} else {
		beats := score.BeatRange{sc.NearestBeat(start), sc.NearestBeat(end)}
		ww.SelectAudio(beats)
	}
}

func (ww *WaveWidget) ShuntSel(Δbeat int) {
	sc := ww.score
	br, ok := ww.selection.(score.BeatRange)
	if ok && sc != nil {
		ww.SelectAudio(sc.Shunt(br, Δbeat))
	}
}

func (ww *WaveWidget) SelectedTimeRange() TimeRange {
	return ww.selection
}

func (ww *WaveWidget) SetWaveform(wav *wave.Waveform) {
	if ww.wav != nil {
		ww.wav.CacheIgnore(ww.iolisten)
	}
	ww.wav = wav
	if ww.wav != nil {
		iolisten := ww.wav.CacheListen()
		ww.iolisten = iolisten
		go func() {
			for {
				chunk, ok := <-iolisten
				if !ok {
					return
				}
				frng:= ww.VisibleFrameRange()
				s0, sN := ww.wav.SampleRange(frng.MinFrame(), frng.MaxFrame())
				if chunk.Intersects(s0, sN) {
					ww.changed(WAV, chunk)
				}
			}
		}()
	}
	ww.changed(WAV | VIEWPOS, wav)
}

func (ww *WaveWidget) SetScore(sc *score.Score) {
	if ww.score != nil {
		ww.score.Unsub(ww)
	}
	ww.score = sc
	if sc != nil {
		events := make(chan interface{})
		ww.score.Sub(ww, events)
		go func() {
			for ev := range events {
				change := SCALE
				if _, ok := ev.(score.BeatChanged); ok {
					change |= BEATS
				}
				if ev, ok := ev.(score.StaffChanged); ok {
					for note, staff := range ww.notesel {
						if _, ok := ev.Staves[staff]; !ok {
							continue
						}
						if staff.NoteAt(note) != note {
							/* note has been removed from staff */
							delete(ww.notesel, note)
						}
					}
					if len(sc.Staves()) != len(ww.rect.staves) {
						change |= LAYOUT
					}
				}
				// XXX could avoid redraw if the staff/beats aren't visible...
				ww.changed(change, ev)
			}
		}()
		selxn := make(chan interface{})
		G.plumb.selection.Sub(&sc, selxn)
		sc.InitQuantizer(selxn)
	}
	ww.changed(SCALE | LAYOUT, sc)
}

func (ww *WaveWidget) SelectedNotes() []score.StaffNote {
	notes := make([]score.StaffNote, 0, len(ww.notesel))
	for note, staff := range ww.notesel {
		notes = append(notes, score.StaffNote{staff, note})
	}
	return notes
}

func (ww *WaveWidget) VisibleFrameRange() FrameRange {
	w0 := ww.first_frame
	wN := w0 + FrameN(ww.frames_per_pixel) * FrameN(ww.rect.wave.Dx())
	return FrameRange{w0, wN}
}

func (ww *WaveWidget) SetCursorByFrame(frame FrameN) {
	ww.cursorX = ww.PixelAtFrame(frame)
	ww.changed(CURSOR, frame)
}

func (ww *WaveWidget) NFrames() FrameN {
	if ww.wav == nil {
		/* TODO allow score without wave */
		return 0
	}
	return ww.wav.ToFrame(ww.wav.NSamples)
}

func (ww *WaveWidget) FrameAtCursor() FrameN {
       return ww.FrameAtPixel(ww.cursorX)
}

func (ww *WaveWidget) FrameAtPixel(x int) FrameN {
	dx := x - ww.rect.wave.Min.X
	return ww.first_frame + FrameN(dx * ww.frames_per_pixel)
}

func (ww *WaveWidget) PixelAtFrame(frame FrameN) int {
	/* TODO rounding */
	return ww.rect.wave.Min.X + int(frame - ww.first_frame) / ww.frames_per_pixel
}

func (ww *WaveWidget) beatDrag(beat *score.BeatRef) DragFn {
	prev, next := beat.Prev(), beat.Next()
	min, max := FrameN(0), ww.NFrames()
	if next != nil {
		max = next.Frame()
	}
	if prev != nil {
		min = prev.Frame()
	}
	return func(pos image.Point, finished bool, moved bool) bool {
		f := ww.FrameAtPixel(pos.X)
		if f <= min || f >= max || !moved {
			return false
		}
		ww.score.MvBeat(beat, f)
		return true
	}
}

func (ww *WaveWidget) timeSelectDrag(anchor FrameN, snap bool) DragFn {
	return func(pos image.Point, finished bool, moved bool)bool {
		if !moved {
			return false
		}
		min := ww.FrameAtPixel(pos.X)
		max := anchor
		if max < min {
			min, max = max, min
		}
		if snap {
			ww.SelectAudioSnapToBeats(min, max)
		} else {
			ww.SelectAudio(FrameRange{min, max})
		}
		return true
	}
}

func (ww *WaveWidget) noteDrag(staff *score.Staff, note *score.Note) DragFn {
	sc := ww.score
	return func(pos image.Point, finished bool, moved bool)bool {
		prospect := ww.noteAtPixel(staff, pos)
		if prospect == nil {
			return false
		}
		Δpitch := int8(staff.PitchForLine(prospect.delta) - note.Pitch)
		beat, offset := sc.Quantize(prospect.beatf)
		Δbeat := Δb(beat, offset, note.Beat, note.Offset)
		_, selected := ww.notesel[note]
		if finished {
			ww.mouse.state = nil
			if moved {
				if selected {
					sc.MvNotes(Δpitch, Δbeat, ww.SelectedNotes()...)
				} else {
					sc.MvNotes(Δpitch, Δbeat, score.StaffNote{staff, note})
				}
			} else {
				/* regular click */
				_, selected := ww.notesel[note]
				if !selected {
					ww.notesel[note] = staff
				} else {
					delete(ww.notesel, note)
				}
				ww.changed(SCALE, ww.notesel)
			}
		} else {
			if selected {
				ww.getMouseState(pos).ndelta = &noteDrag{Δpitch, Δbeat}
			} else {
				ww.getMouseState(pos).note = prospect
			}
			ww.changed(SCALE, prospect)
		}
		return true
	}
}

func (ww *WaveWidget) noteSelectDrag(start image.Point) DragFn {
	// XXX funny interaction with scrolling because we hold on to pixel values
	sc := ww.score
	addToSel := G.kb.shift
	return func(end image.Point, finished bool, moved bool)bool {
		r := image.Rectangle{start, end}.Canon()
		if !finished {
			ww.getMouseState(end).rectSelect = &r
			ww.changed(SCALE, r)
		} else {
			if !(addToSel || G.kb.shift) {
				for note, _ := range ww.notesel {
					delete(ww.notesel, note)
				}
			}
			var sn score.StaffNote
			next := sc.Iter(ww.VisibleFrameRange())
			for next != nil {
				sn, next = next()
				dn := ww.dispNote(sn.Staff, sn.Note, centerPt(ww.rect.staves[sn.Staff]).Y)
				if dn.pt != nil && dn.pt.In(r) {
					ww.notesel[sn.Note] = sn.Staff
				}
			}
			ww.getMouseState(end).rectSelect = nil
			ww.changed(SCALE, &ww.notesel)
		}
		return true
	}
}

func (ww *WaveWidget) dragState(mouse image.Point) (DragFn, wde.Cursor) {
	beath := 8
	grabw := 2
	sc := ww.score
	r := ww.Rect()
	bAxis, tAxis := mouse.In(ww.rect.beatAxis), mouse.In(ww.rect.timeAxis)
	snap := bAxis && sc != nil && sc.HasBeats()
	if mouse.In(padRect(vrect(r, ww.PixelAtFrame(ww.selection.MinFrame())), grabw, 0)) {
		return ww.timeSelectDrag(ww.selection.MaxFrame(), snap), wde.ResizeWCursor
	}
	if mouse.In(padRect(vrect(r, ww.PixelAtFrame(ww.selection.MaxFrame())), grabw, 0)) {
		return ww.timeSelectDrag(ww.selection.MinFrame(), snap), wde.ResizeECursor
	}
	if bAxis || tAxis {
		return ww.timeSelectDrag(ww.FrameAtPixel(mouse.X), snap), wde.IBeamCursor
	}

	rng := FrameRange{ww.FrameAtPixel(mouse.X - yspacing*2), ww.FrameAtPixel(mouse.X + yspacing*2)}
	for staff, rect := range ww.rect.staves {
		if mix, ok := ww.rect.mixers[staff]; (ok && mix.Minimised) || !mouse.In(rect) {
			continue
		}
		mid := rect.Min.Y + rect.Dy() / 2
		next := sc.Iter(rng, staff)
		var sn score.StaffNote
		for next != nil {
			sn, next = next()
			frame, _ := sc.ToFrame(sc.Beatf(sn.Note))
			x := ww.PixelAtFrame(frame)
			delta, _ := staff.LineForPitch(sn.Note.Pitch)
			y := mid - (yspacing / 2) * (delta)
			r := padPt(image.Pt(x, y), yspacing / 2, yspacing / 2)
			// XXX would be good to target the closest note instead of the first
			if mouse.In(r) {
				return ww.noteDrag(staff, sn.Note), wde.GrabHoverCursor
			}
		}
	}

	// TODO ignore beat grabs when sufficiently zoomed out
	if sc != nil && mouse.Y <= ww.rect.wave.Min.Y + beath {
		beat := sc.NearestBeat(ww.FrameAtPixel(mouse.X))
		if beat != nil {
			x := ww.PixelAtFrame(beat.Frame())
			if x - grabw <= mouse.X && mouse.X <= x + grabw {
				return ww.beatDrag(beat), wde.ResizeEWCursor
			}
		}
	}

	if mouse.In(ww.rect.wave) {
		return ww.noteSelectDrag(mouse), wde.NormalCursor
	}

	return nil, wde.NormalCursor
}

func (ww *WaveWidget) staffContaining(pos image.Point) *score.Staff {
	for staff, rect := range ww.rect.staves {
		if pos.In(rect) {
			return staff
		}
	}
	return nil
}

func (ww *WaveWidget) noteAtPixel(staff *score.Staff, pos image.Point) *noteProspect {
	rect := ww.rect.staves[staff]
	mid := rect.Min.Y + rect.Dy() / 2
	noteY := snapto(pos.Y, mid, yspacing / 2)
	delta := (mid - noteY) / (yspacing / 2)

	frame := ww.FrameAtPixel(pos.X)
	beatf, ok := ww.score.ToBeat(frame)
	if !ok {
		return nil
	}

	return &noteProspect{delta, beatf, staff}
}

func (ww *WaveWidget) getMouseState(pos image.Point) *mouseState {
	state := ww.mouse.state
	cachedPos := ww.mouse.pos
	if state != nil && pos.Eq(cachedPos) {
		return state
	}
	state = ww.calcMouseState(pos)
	ww.mouse.state = state
	ww.mouse.pos = pos

	return state
}

func (ww *WaveWidget) calcMouseState(pos image.Point) *mouseState {
	state := new(mouseState)

	dragFn, cursor := ww.dragState(pos)
	state.dragFn = dragFn
	state.cursor = cursor

	staff := ww.staffContaining(pos)
	if staff == nil {
		state.note = nil;
	} else {
		state.note = ww.noteAtPixel(staff, pos)
	}

	return state
}

func (ww *WaveWidget) MouseMoved(mousePos image.Point) wde.Cursor {
	orig := ww.mouse.state
	s := ww.getMouseState(mousePos)
	if s.note != nil && (orig == nil || orig.note == nil || !s.note.Eq(orig.note)) {
		ww.changed(SCALE, mousePos)
	}
	if !audio.IsPlaying() && ww.cursorX != mousePos.X && mousePos.X > ww.rect.wave.Min.X {
		ww.cursorX = mousePos.X
		ww.changed(CURSOR, ww.cursorX)
	}
	return s.cursor
}

func (ww *WaveWidget) mkNote(prospect *noteProspect, dur *big.Rat) *score.Note {
	beat, offset := ww.score.Quantize(prospect.beatf)
	return &score.Note{prospect.staff.PitchForLine(prospect.delta), dur, beat, offset}
}

func (ww *WaveWidget) LeftClick(mouse image.Point) {
	if mouse.In(ww.rect.newStaffB) && ww.score != nil {
		ww.score.AddStaff(score.MkStaff("", &score.TrebleClef, ww.score.Key()))
		return
	}
	for staff, layout := range ww.rect.mixers {
		if mouse.In(layout.r) {
			if mouse.In(layout.muteB) {
				toggle(&Mixer.For(staff).Muted)
				ww.changed(MIXER, ww)
			} else if mouse.In(layout.minmaxB) {
				toggle(&layout.Minimised)
				ww.changed(LAYOUT | SCALE, &layout.Minimised)
			}
		}
	}
}

func (ww *WaveWidget) RightClick(mouse image.Point) {
	if mouse.In(ww.rect.mixer) {
		if mouse.In(ww.rect.newStaffB) && ww.score != nil {
			ww.score.AddStaff(score.MkStaff("", &score.BassClef, ww.score.Key()))
			return
		}
		for staff, _ := range ww.rect.staves {
			layout := ww.rect.mixers[staff]
			if mouse.In(layout.minmaxB) {
				layout.Minimised = false
				for staff2, layout2 := range ww.rect.mixers {
					if staff2 != staff {
						layout2.Minimised = true
					}
				}
				ww.changed(LAYOUT | SCALE, &layout.Minimised)
				return
			}
		}
	}
	if ww.pasteMode {
		sc := ww.score
		if s := ww.getMouseState(mouse); s != nil && len(ww.snarf[s.note.staff]) > 0 {
			anchor := ww.snarf[s.note.staff][0]
			Δpitch := int8(s.note.staff.PitchForLine(s.note.delta) - anchor.Pitch)
			beat, offset := sc.Quantize(s.note.beatf)
			Δbeat := Δb(beat, offset, anchor.Beat, anchor.Offset)
			for staff, notes := range ww.snarf {
				mv := make([]*score.Note, 0, len(notes))
				for _, note := range notes {
					mv = append(mv, note.Dup().Mv(Δpitch, Δbeat))
				}
				sc.AddNotes(staff, mv...)
			}
			ww.pasteMode = false
		}
	}
}

func (ww *WaveWidget) Scroll(amount float64) int {
	return ww.ScrollPixels(int(float64(ww.rect.wave.Dx()) * amount))
}

func (ww *WaveWidget) ScrollPixels(dx int) int {
	if ww.Rect().Empty() || ww.wav == nil {
		return 0
	}
	original := ww.first_frame
	shift := FrameN(dx * ww.frames_per_pixel)
	rbound := ww.NFrames() - FrameN((ww.rect.wave.Dx() + 1) * ww.frames_per_pixel)
	ww.first_frame += shift
	if ww.first_frame < 0 || rbound < 0 {
		ww.first_frame = 0
	} else if ww.first_frame > rbound {
		ww.first_frame = rbound
	}
	diff := int(ww.first_frame - original)
	if diff != 0 {
		ww.mouse.state = nil
		ww.changed(WAV | CURSOR | VIEWPOS, ww.first_frame)
	}
	return diff
}

func (ww *WaveWidget) Zoom(factor float64) float64 {
	fpp := int(float64(ww.frames_per_pixel) * factor)
	if fpp < 1 {
		fpp = 1
	}
	if wav := ww.wav; wav != nil && ww.rect.wave.Dx() > 0 {
		max_frames := (12*1024*1024 / 10 / 2 / 2) * 9
		max_fpp := int(max_frames / ww.rect.wave.Dx())
		if fpp > max_fpp {
			fpp = max_fpp
		}
	}
	delta := float64(fpp) / float64(ww.frames_per_pixel)
	if delta != 1.0 {
		/* XXX should probably only account for cursor when mouse is over widget */
		x := ww.mouse.pos.X
		frameAtMouse := ww.FrameAtPixel(x)
		dx := x - ww.rect.wave.Min.X
		ww.first_frame = frameAtMouse - FrameN(dx * fpp)
		ww.frames_per_pixel = fpp
		ww.mouse.state = nil
		ww.changed(WAV | CURSOR | VIEWPOS, fpp)
	}
	return delta
}

func (ww *WaveWidget) Snarf() {
	snarf := make(map[*score.Staff] []*score.Note)
	for note, staff := range ww.notesel {
		snarf[staff] = score.Merge(snarf[staff], note.Dup())
	}
	ww.snarf = snarf
}

func (ww *WaveWidget) PasteMode() bool {
	return ww.pasteMode
}

func (ww *WaveWidget) SetPasteMode(mode bool) {
	if ww.pasteMode == mode || (mode && ww.snarf == nil) {
		return
	}
	ww.pasteMode = mode
	ww.changed(SCALE, ww.snarf)
}

func (ww *WaveWidget) TimeAtCursor(x int) time.Duration {
	if ww.wav == nil {
		return 0.0
	}
	frame := ww.FrameAtPixel(x)
	return ww.wav.TimeAtFrame(frame)
}

func (ww *WaveWidget) Status() string {
	s := ww.getMouseState(ww.mouse.pos)
	pitch := uint8(0)
	delta := 0
	delta2 := 0
	offset := big.NewRat(1, 1)
	nsharps := score.KeySig(-99)
	if s.note != nil {
		beatf := s.note.beatf
		delta = s.note.delta
		_, offset = ww.score.Quantize(beatf)
		pitch = s.note.staff.PitchForLine(delta)
		delta2, _ = s.note.staff.LineForPitch(pitch)
		nsharps = ww.score.Key()
	}

	return fmt.Sprintf("line=%d (%d) pitch=%d %s offset=%v %v %v", delta, delta2, pitch, midi.PitchName(pitch), offset, nsharps, len(ww.notesel))
}
