package main

import (
	"bytes"
	"io"
	"sync"
	"time"
)

type BufferedWriter struct {
	writer     io.Writer
	buffer     *bytes.Buffer
	bufferSize int
	flushDelay time.Duration
	mu         sync.Mutex
	done       chan struct{}
}

func NewBufferedWriter(w io.Writer, bufferSize int, flushDelay time.Duration) *BufferedWriter {
	bw := &BufferedWriter{
		writer:     w,
		buffer:     bytes.NewBuffer(make([]byte, 0, bufferSize)),
		bufferSize: bufferSize,
		flushDelay: flushDelay,
		done:       make(chan struct{}),
	}

	go bw.flushLoop()
	return bw
}

func (w *BufferedWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err = w.buffer.Write(p)
	if err != nil {
		return n, err
	}

	if w.buffer.Len() >= w.bufferSize {
		err = w.flush()
	}

	return n, err
}

func (w *BufferedWriter) flush() error {
	if w.buffer.Len() == 0 {
		return nil
	}

	_, err := w.writer.Write(w.buffer.Bytes())
	w.buffer.Reset()
	return err
}

func (w *BufferedWriter) flushLoop() {
	ticker := time.NewTicker(w.flushDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			w.flush()
			w.mu.Unlock()
		case <-w.done:
			return
		}
	}
}

func (w *BufferedWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	close(w.done)
	return w.flush()
}
