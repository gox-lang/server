package toolchain

import (
	"io"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/logs"
)

type progressReader struct {
	reader      io.Reader
	label       string
	total       int64
	current     int64
	lastPrint   time.Time
	lastPercent int
	done        bool
}

func newProgressReader(reader io.Reader, label string, total int64) *progressReader {
	return &progressReader{
		reader:      reader,
		label:       label,
		total:       total,
		lastPercent: -1,
	}
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)

	if n > 0 {
		p.current += int64(n)
		p.render(false)
	}

	if err == io.EOF {
		p.render(true)
	}

	return n, err
}

func (p *progressReader) render(force bool) {
	if p.done {
		return
	}

	if p.total <= 0 {
		if !force && time.Since(p.lastPrint) < 250*time.Millisecond {
			return
		}

		p.lastPrint = time.Now()
		logs.Progress(p.label, "%d bytes", p.current)

		if force {
			logs.ProgressDone()
			p.done = true
		}

		return
	}

	percent := float64(p.current) / float64(p.total) * 100
	if percent > 100 {
		percent = 100
	}

	percentStep := int(percent)
	if !force {
		if percentStep == p.lastPercent {
			return
		}

		if time.Since(p.lastPrint) < 80*time.Millisecond {
			return
		}
	}

	p.lastPrint = time.Now()
	p.lastPercent = percentStep

	width := 16
	filled := int((percent / 100) * float64(width))

	if filled > width {
		filled = width
	}

	bar := strings.Repeat("=", filled) + strings.Repeat("-", width-filled)

	logs.Progress(p.label, "[%s] %6.2f%%", bar, percent)

	if force {
		logs.ProgressDone()
		p.done = true
	}
}

type progressSpinner struct {
	label string
	stop  chan struct{}
	done  chan struct{}
}

func startProgressSpinner(label string) *progressSpinner {
	p := &progressSpinner{label: label, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		width := 16
		step := 0
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-p.stop:
				bar := strings.Repeat("=", width)
				logs.Progress(label, "[%s] %6.2f%%", bar, 100.0)
				logs.ProgressDone()
				return
			case <-ticker.C:
				step++
				percent := float64((step%15)+1) / 16.0 * 95.0
				filled := int((percent / 100.0) * float64(width))
				if filled < 1 {
					filled = 1
				}
				if filled > width-1 {
					filled = width - 1
				}
				bar := strings.Repeat("=", filled) + strings.Repeat("-", width-filled)
				logs.Progress(label, "[%s] %6.2f%%", bar, percent)
			}
		}
	}()
	return p
}

func (p *progressSpinner) Finish() {
	if p == nil {
		return
	}
	close(p.stop)
	<-p.done
}
