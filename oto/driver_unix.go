// Copyright 2021 The Oto Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !android && !darwin && !js && !windows && !nintendosdk && !playstation5

package oto

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jfreymuth/pulse"

	"github.com/ebitengine/oto/v3/internal/mux"
)

type context struct {
	client *pulse.Client
	stream *pulse.PlaybackStream

	suspended bool
	cond      *sync.Cond

	mux *mux.Mux
	err atomicError

	channelCount      int
	bufferSizeInBytes int
	applicationName   string
}

func newContext(sampleRate int, channelCount int, format mux.Format, bufferSizeInBytes int, applicationName string) (client *context, ready chan struct{}, err error) {
	client = &context{
		cond: sync.NewCond(&sync.Mutex{}),
		mux:  mux.New(sampleRate, channelCount, format),
	}
	ready = make(chan struct{})
	close(ready)
	defer func() {
		if client != nil && client.client != nil && err != nil {
			client.client.Close()
		}
	}()

	if applicationName == "" {
		if name, _ := os.Executable(); name != "" {
			applicationName = filepath.Base(name)
		} else {
			applicationName = "Oto"
		}
	}
	client.channelCount = channelCount
	client.bufferSizeInBytes = bufferSizeInBytes
	client.applicationName = applicationName

	client.client, err = pulse.NewClient(pulse.ClientApplicationName(applicationName))
	if err != nil {
		return nil, ready, fmt.Errorf("oto: PulseAudio client initialization failed: %w", err)
	}

	options := []pulse.PlaybackOption{
		pulse.PlaybackMediaName(applicationName),
	}
	switch channelCount {
	case 1:
		options = append(options, pulse.PlaybackMono)
	case 2:
		options = append(options, pulse.PlaybackStereo)
	default:
		return nil, ready, fmt.Errorf("oto: PulseAudio backend supports only mono or stereo output: %d", channelCount)
	}
	options = append(options, pulse.PlaybackSampleRate(sampleRate))
	{
		latency := float64(bufferSizeInBytes) / float64(sampleRate*channelCount*4)
		if latency <= 0 {
			// If no buffer size is specified, default to a 100ms latency.
			// Without this, PulseAudio uses its own large default buffer (~2s),
			// which causes a noticeable delay before audio starts playing.
			latency = 0.1
		}
		options = append(options, pulse.PlaybackLatency(latency))
	}

	client.stream, err = client.client.NewPlayback(pulse.Float32Reader(client.read), options...)
	if err != nil {
		return nil, ready, fmt.Errorf("oto: PulseAudio playback initialization failed: %w", err)
	}
	client.stream.Start()

	return client, ready, nil
}

func (c *context) read(buf []float32) (int, error) {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()

	for c.suspended && c.err.Load() == nil {
		c.cond.Wait()
	}
	if err := c.err.Load(); err != nil {
		return 0, err
	}

	c.mux.ReadFloat32s(buf)
	return len(buf), nil
}

func (c *context) Suspend() error {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()

	if err := c.err.Load(); err != nil {
		return err
	}
	if err := c.stream.Error(); err != nil {
		return fmt.Errorf("oto: PulseAudio error: %w", err)
	}

	c.suspended = true
	c.stream.Pause()
	return nil
}

func (c *context) Resume() error {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()

	if err := c.err.Load(); err != nil {
		return err
	}
	if err := c.stream.Error(); err != nil {
		return fmt.Errorf("oto: PulseAudio error: %w", err)
	}

	c.suspended = false
	c.stream.Resume()
	c.cond.Signal()
	return nil
}

func (c *context) Err() error {
	if err := c.err.Load(); err != nil {
		return err
	}
	if err := c.stream.Error(); err != nil {
		return fmt.Errorf("oto: PulseAudio error: %w", err)
	}
	return nil
}

// SetSampleRate dynamically changes the sample rate of the underlying PulseAudio
// playback stream. The PulseAudio stream is recreated with the new sample rate,
// causing a brief gap in audio output (typically 5-50ms). Callers should hold
// the appropriate lock and pause playback to avoid glitches.
func (c *context) SetSampleRate(sampleRate int) error {
	c.cond.L.Lock()
	if err := c.err.Load(); err != nil {
		c.cond.L.Unlock()
		return err
	}
	if err := c.stream.Error(); err != nil {
		c.cond.L.Unlock()
		return fmt.Errorf("oto: PulseAudio error: %w", err)
	}
	if c.suspended {
		c.cond.L.Unlock()
		return fmt.Errorf("oto: cannot set sample rate while suspended")
	}
	oldStream := c.stream
	c.cond.L.Unlock()

	oldStream.Stop()
	oldStream.Drain()
	oldStream.Close()

	options := []pulse.PlaybackOption{
		pulse.PlaybackMediaName(c.applicationName),
	}
	switch c.channelCount {
	case 1:
		options = append(options, pulse.PlaybackMono)
	case 2:
		options = append(options, pulse.PlaybackStereo)
	default:
		return fmt.Errorf("oto: PulseAudio backend supports only mono or stereo output: %d", c.channelCount)
	}
	options = append(options, pulse.PlaybackSampleRate(sampleRate))
	{
		latency := float64(c.bufferSizeInBytes) / float64(sampleRate*c.channelCount*4)
		if latency <= 0 {
			latency = 0.1
		}
		options = append(options, pulse.PlaybackLatency(latency))
	}

	var err error
	newStream, err := c.client.NewPlayback(pulse.Float32Reader(c.read), options...)
	if err != nil {
		return fmt.Errorf("oto: PulseAudio playback reinitialization failed: %w", err)
	}
	newStream.Start()

	c.cond.L.Lock()
	c.stream = newStream
	c.cond.L.Unlock()

	return nil
}
