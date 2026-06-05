// Copyright 2024 The Oto Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build !android && !darwin && !js && !windows && !nintendosdk && !playstation5

package oto

// SetSampleRate dynamically changes the sample rate of the underlying audio stream.
// This allows switching to audio sources with different sample rates without
// reinitializing the entire audio context.
// Currently only implemented on Linux (PulseAudio backend).
//
// SetSampleRate is concurrent-safe.
func (c *Context) SetSampleRate(sampleRate int) error {
	return c.context.SetSampleRate(sampleRate)
}
