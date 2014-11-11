package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"sync"
	"time"

	"github.com/skelterjohn/go.wde"
	_ "github.com/skelterjohn/go.wde/init"
	"sqweek.net/sqribe/audio"
	"sqweek.net/sqribe/score"
)

var cursorCtl CursorCtl

func toggle(flag *bool) {
	*flag = !*flag
}

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
				if !audio.IsPlaying() {
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
				G.score.KeyChange(-1)
			case wde.KeyF3:
				G.score.KeyChange(1)
			case wde.KeyPrior:
				G.mixer.waveBias.Shunt(0.1)
			case wde.KeyNext:
				G.mixer.waveBias.Shunt(-0.1)
			case wde.KeySpace:
				playToggle()
			case wde.KeyReturn:
				if s, playing := audio.PlayingSample(); playing {
					G.score.AddBeat(G.wav.ToFrame(s))
				}
			case wde.KeyDelete:
				rng := G.ww.GetSelectedTimeRange()
				if beats, ok := rng.(score.BeatRange); ok {
					G.score.RemoveNotes(beats)
				}
			default:
				switch e.Glyph {
				case "%":
					rng := G.ww.GetSelectedTimeRange()
					if beats, ok := rng.(score.BeatRange); ok {
						G.score.RepeatNotes(beats)
					}
				case "s", "S":
					SaveState(G.audiofile)
				case "t", "T":
					toggle(&G.mixer.metronome)
				case "a", "A":
					toggle(&audio.Mixer.MuteAudio)
				case "m", "M":
					toggle(&audio.Mixer.MuteMidi)
				case "q", "Q":
					go G.score.QuantizeBeats()
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
	q := G.score.QuantizeBeatStat()
	if q.Nop() {
		return ""
	}
	bpm := 60.0 * float64(time.Second) / float64(G.wav.TimeAtFrame(q.AvgFramesPerBeat()))
	errd := G.wav.TimeAtFrame(*q.Error)
	return fmt.Sprintf("%.1fbpm ±%v", bpm, niceDur(errd))
}

func drawstatus(dst draw.Image, r image.Rectangle) {
	bg := color.RGBA{0xcc, 0xcc, 0xcc, 0xff}
	draw.Draw(dst, r, &image.Uniform{bg}, image.ZP, draw.Src)
	G.font.luxi.Draw(dst, color.Black, r, fmt.Sprintf("%s  %v", G.ww.Status(), quantizeStr()))
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

	G.mixer.waveBias = NewSlider(&audio.Mixer.Bias, -0.5, 0.5, redraw)

	go drawstuff(dw, redraw, done)
	go event(dw.EventChan(), redraw, done, &wg)

	return &wg
}
