package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"log"
	"sync"
	"time"

	"github.com/skelterjohn/go.wde"
	_ "github.com/skelterjohn/go.wde/init"
)

var cursorCtl CursorCtl

func event(events <-chan interface{}, redraw chan Widget, done chan bool, wg *sync.WaitGroup) {
	defer func() {
		done <- true
		wg.Done()
	}()
	var drag DragFn = nil
	var dragged bool = false
	var refreshTimer *time.Timer
	for ei := range events {
		switch e := ei.(type) {
		case wde.MouseDownEvent:
			dragged = false
			switch (e.Which) {
			case wde.LeftButton:
				if e.Where.In(G.ww.Rect()) {
					drag, _ = G.ww.CursorIconAtPixel(e.Where)
				}
			case wde.RightButton:
				if e.Where.In(G.ww.Rect()) {
					G.ww.RightButtonDown(e.Where)
				}
			case wde.WheelUpButton:
				G.ww.Zoom(0.75)
			case wde.WheelDownButton:
				G.ww.Zoom(1.50)
			}
		case wde.MouseUpEvent:
			if dragged {
				drag(e.Where, true)
				continue
			}
			switch (e.Which) {
			case wde.LeftButton:
				if e.Where.In(G.ww.Rect()) {
					G.ww.LeftClick(e.Where)
				} else if e.Where.In(G.mixer.waveBias.Rect()) {
					G.mixer.waveBias.LeftClick(e.Where)
				}
			case wde.RightButton:
				if !G.noteMenu.Rect().Empty() {
					G.noteMenu.RightButtonUp(e.Where)
				} else if e.Where.In(G.mixer.waveBias.Rect()) {
					G.mixer.waveBias.RightClick(e.Where)
				}
			}
		case wde.MouseDraggedEvent:
			dragged = true
			switch (e.Which) {
			case wde.LeftButton:
				if drag != nil {
					drag(e.Where, false)
				}
			case wde.RightButton:
				if !G.noteMenu.Rect().Empty() {
					G.noteMenu.MouseMoved(e.Where)
				}
			}
		case wde.MouseMovedEvent:
			if e.Where.In(G.noteMenu.Rect()) {
				G.noteMenu.MouseMoved(e.Where)
			} else if e.Where.In(G.ww.Rect()) {
				if !IsPlaying() {
					G.ww.MouseMoved(e.Where)
				}
				_, cur := G.ww.CursorIconAtPixel(e.Where)
				cursorCtl.Set(cur)
			} else {
				cursorCtl.Set(NormalCursor)
			}
		case wde.KeyTypedEvent:
			log.Println("typed", e.Key, e.Glyph, e.Chord)
			switch e.Key {
			case wde.KeyLeftArrow:
				G.ww.Scroll(-0.25)
			case wde.KeyRightArrow:
				G.ww.Scroll(0.25)
			case wde.KeyUpArrow:
				G.ww.Zoom(0.5)
			case wde.KeyDownArrow:
				G.ww.Zoom(2.0)
			case wde.KeyF2:
				for _, staff := range(G.score.staves) {
					staff.nsharps--
				}
			case wde.KeyF3:
				for _, staff := range(G.score.staves) {
					staff.nsharps++
				}
			case wde.KeyPrior:
				G.mixer.waveBias.Shunt(0.1)
			case wde.KeyNext:
				G.mixer.waveBias.Shunt(-0.1)
			case wde.KeySpace:
				playToggle()
			case wde.KeyReturn:
				if s, playing := CurrentSample(); playing {
					G.score.AddBeat(G.wav.ToFrame(s))
				}
			case wde.KeyDelete:
				rng := G.ww.GetSelectedTimeRange()
				if beats, ok := rng.(BeatRange); ok {
					G.score.RemoveNotes(beats)
				}
			default:
				switch e.Glyph {
				case "%":
					rng := G.ww.GetSelectedTimeRange()
					if beats, ok := rng.(BeatRange); ok {
						G.score.RepeatNotes(beats)
					}
				case "s", "S":
					SaveState(G.audiofile)
				case "t", "T":
					G.mixer.metronome = !G.mixer.metronome
				case "a", "A":
					G.mixer.audio = !G.mixer.audio
				case "m", "M":
					G.mixer.midi = !G.mixer.midi
				case "q", "Q":
					c := make(chan bool)
					G.quantize.apply <- c
					<-c
				}
			}
		case wde.ResizeEvent:
			if refreshTimer != nil {
				refreshTimer.Stop()
			}
			refreshTimer = time.AfterFunc(50*time.Millisecond, func() {redraw <- nil})
		case wde.CloseEvent:
			return
		}
	}
}

type QuantizeBeats struct {
	beats BeatRange
	b0, bN int
	df float64
	error *FrameN
}

func (q QuantizeBeats) Nop() bool {
	return q.beats.First == nil || q.beats.Last == nil
}

func (q *QuantizeBeats) reset() {
	f0, fN := q.beats.First.frame, q.beats.Last.frame
	/* XXX should be a quantizer per score instead of referencing G.score */
	q.b0, q.bN = G.score.BeatIndex(q.beats.First), G.score.BeatIndex(q.beats.Last)
	q.df = float64(fN - f0) / float64(q.bN - q.b0)
	q.error = nil
}

func intBeat(score *Score, f FrameN) int {
	b, _ := score.ToBeat(f)
	return int(b)
}

func quantizer(selxn chan interface{}, beats chan interface{}, apply chan chan bool, calc chan chan QuantizeBeats, redraw chan Widget) {
	var q QuantizeBeats
	for {
		select {
		case ev := <-beats:
			if _, ok := ev.(BeatChanged); ok && !q.Nop() {
				q.reset()
			}
		case ev := <-selxn:
			if len(G.score.beats) > 0 {
				switch e := ev.(type) {
				case FrameRange:
					q.beats = BeatRange{nil, nil}
				case BeatRange:
					q.beats = e
					q.reset()
				}
			}
		case reply := <-apply:
			if q.Nop() {
				reply <- true
				continue
			}
			fmt.Println("quantize apply:", q)
			f0 := q.beats.First.frame
			for ib := q.b0 + 1; ib <= q.bN - 1; ib++ {
				fmt.Println("FROM", G.score.beats[ib].frame)
				G.score.beats[ib].frame = f0 + FrameN(float64(ib - q.b0) * q.df)
				fmt.Println("  TO", G.score.beats[ib].frame)
			}
			*q.error = 0
			G.plumb.score.C <- BeatChanged{}
			redraw <- G.ww
			reply <- true
		case reply := <-calc:
			if q.Nop() {
				reply <- q
				continue
			}
			if q.error == nil {
				q.error = new(FrameN)
				f0 := q.beats.First.frame
				for ib := q.b0; ib <= q.bN; ib++ {
					qf := f0 + FrameN(float64(ib - q.b0) * q.df)
					af, _ := G.score.ToFrame(float64(ib))
					ef := FrameN(int64(math.Abs(float64(qf - af))))
					if ef > *q.error {
						*q.error = ef
					}
				}
			}
			reply <- q
		}
	}
}

/* rounds sub-second duration to nearest ms/μs/ns */
func niceDur(dur time.Duration) string {
	if dur >= time.Second {
		return dur.String()
	}
	switch {
	case dur >= time.Millisecond:
		return fmt.Sprintf("%dms", int(dur / time.Millisecond))
	case dur >= time.Microsecond:
		return fmt.Sprintf("%dµs", int(dur / time.Microsecond))
	default:
		return fmt.Sprintf("%dns", int(dur))
	}
}

func quantizeStr() string {
	c := make(chan QuantizeBeats)
	G.quantize.calc <- c
	q := <-c
	if q.Nop() {
		return ""
	}
	bpm := 60.0 * float64(time.Second) / (float64(G.wav.TimeAtFrame(q.beats.Last.frame - q.beats.First.frame + 1)) / float64(q.bN - q.b0))
	errd := G.wav.TimeAtFrame(*q.error)
	return fmt.Sprintf("%.1fbpm ±%v", bpm, niceDur(errd))
}

func drawstatus(dst draw.Image, r image.Rectangle) {
	bg := color.RGBA{0xcc, 0xcc, 0xcc, 0xff}
	draw.Draw(dst, r, &image.Uniform{bg}, image.ZP, draw.Src)
	G.font.luxi.Draw(dst, color.Black, r, fmt.Sprintf("%s  %s", G.ww.Status(), quantizeStr()))
}

func drawstuff(w wde.Window, redraw chan Widget, done chan bool) {
	rate := time.Millisecond * 33 /* maximum refresh rate */
	lastframe := time.Now().Add(-rate)
	var refresh func()
	merged := 0
	for {
		select {
		case widget := <-redraw:
			now := time.Now()
			nextframe := lastframe.Add(rate)
			if refresh != nil || now.Before(nextframe) {
				merged++
				if refresh == nil {
					refresh = func() {
						redraw <- widget
						refresh = nil
					}
					time.AfterFunc(nextframe.Sub(now), refresh)
				}
			} else {
				lastframe = now
				width, height := w.Size()
				r := image.Rect(0, 0, width, height)
				img := image.NewRGBA(r)
				wvR := image.Rect(0, int(0.2*float32(height)), width, int(0.8*float32(height) + 20))
				G.ww.Draw(img, wvR)

				mixR := image.Rect(width - 50, wvR.Min.Y - 15, width, wvR.Min.Y)
				G.mixer.waveBias.Draw(img, mixR)

				statusR := image.Rect(0, wvR.Max.Y, width, height)
				drawstatus(img, statusR)

				if !G.noteMenu.Rect().Empty() {
					G.noteMenu.Draw(img, G.noteMenu.Rect())
				}
				w.Screen().CopyRGBA(img, r)
				w.FlushImage()
				//log.Println("redraw took ", time.Now().Sub(lastframe), "  merged: ", merged)
				merged = 0
				lastframe = time.Now()
			}
		case <-done:
			return
		}
	}
}

func InitWde(redraw chan Widget) *sync.WaitGroup {
	dw, err := wde.NewWindow(600, 400)
	if err != nil {
		log.Fatal(err)
	}
	dw.SetTitle("Sqribe")
	dw.SetSize(600, 400)
	dw.Show()

	wg := sync.WaitGroup{}
	wg.Add(1)

	cursorCtl = NewCursorCtl(dw)
	done := make(chan bool)

	selxn, beats := make(chan interface{}), make(chan interface{})
	G.plumb.selection.Sub(&G.quantize, selxn)
	G.plumb.score.Sub(&G.quantize, beats)
	go quantizer(selxn, beats, G.quantize.apply, G.quantize.calc, redraw)

	G.mixer.waveBias = NewSlider(0.5, redraw)

	go drawstuff(dw, redraw, done)
	go event(dw.EventChan(), redraw, done, &wg)

	return &wg
}
