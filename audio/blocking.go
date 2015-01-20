package audio

import (
	"code.google.com/p/portaudio-go/portaudio"
	"time"

	. "sqweek.net/sqribe/core/types"
)

type blockingOps struct {
	buf []int16
	pos int
	playbackStart time.Duration
}

func blockOps(channels int) *blockingOps {
	return &blockingOps{buf: make([]int16, 1024 * channels)}
}

func (block *blockingOps) Open(params portaudio.StreamParameters) (*portaudio.Stream, error) {
	return portaudio.OpenStream(params, block.buf)
}

func (block *blockingOps) Append(wav []int16) int {
	src := wav
	for len(src) > 0 {
		n := copy(block.buf[block.pos:], src)
		src = src[n:]
		block.pos += n
		if block.pos == len(block.buf) {
			stream.Write()
			block.pos = 0
		}
	}
	return len(wav)
}

func (block *blockingOps) Start() {
	block.playbackStart = monotonicTime()
}

func (block *blockingOps) Index() (SampleN, bool) {
	dt := monotonicTime() - block.playbackStart
	return SampleN(samplesPerSecond * dt.Seconds()), true
}
