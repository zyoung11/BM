package main

import (
	"fmt"

	"github.com/gopxl/beep/v2"
	resampling "github.com/tphakala/go-audio-resampler"
)

// getResamplingQuality converts the quality string from config to the corresponding resampling quality preset.
func getResamplingQuality(quality string) resampling.QualityPreset {
	switch quality {
	case "quick":
		return resampling.QualityQuick
	case "low":
		return resampling.QualityLow
	case "medium":
		return resampling.QualityMedium
	case "high":
		return resampling.QualityHigh
	case "very_high":
		return resampling.QualityVeryHigh
	default:
		// Default to very_high if invalid value
		return resampling.QualityVeryHigh
	}
}

// highQualityResample performs high-quality audio resampling using go-audio-resampler
// with the quality preset specified in the configuration.
func highQualityResample(streamer beep.Streamer, inputRate, outputRate beep.SampleRate) (beep.StreamSeeker, error) {
	// Convert sample rates to float64 for the resampler
	inputRateHz := float64(inputRate)
	outputRateHz := float64(outputRate)

	// Get the quality preset from configuration
	qualityPreset := getResamplingQuality(GlobalConfig.App.ResamplingQuality)

	// Read all samples from the streamer
	var inputSamples []float64
	buffer := make([][2]float64, 512)

	for {
		n, ok := streamer.Stream(buffer)
		if !ok {
			break
		}

		// Convert stereo samples to mono for processing (average left and right channels)
		for i := 0; i < n; i++ {
			monoSample := (buffer[i][0] + buffer[i][1]) / 2.0
			inputSamples = append(inputSamples, monoSample)
		}
	}

	// Resample the audio using the configured quality preset
	outputSamples, err := resampling.ResampleMono(inputSamples, inputRateHz, outputRateHz, qualityPreset)
	if err != nil {
		return nil, fmt.Errorf("failed to resample audio: %v", err)
	}

	// Create a buffer with the resampled audio
	bufferFormat := beep.Format{
		SampleRate:  outputRate,
		NumChannels: 2,
		Precision:   3, // 24-bit audio
	}

	// Create a custom streamer that returns the resampled stereo samples
	resampledStreamer := &resampledStreamer{
		samples: outputSamples,
		index:   0,
	}

	// Create buffer and append the streamer
	audioBuffer := beep.NewBuffer(bufferFormat)
	audioBuffer.Append(resampledStreamer)

	return audioBuffer.Streamer(0, audioBuffer.Len()), nil
}

// resampledStreamer implements the beep.Streamer interface for resampled mono audio
// converted to stereo by duplicating the mono channel to both left and right.
type resampledStreamer struct {
	samples []float64
	index   int
}

func (r *resampledStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	if r.index >= len(r.samples) {
		return 0, false
	}

	for i := range samples {
		if r.index >= len(r.samples) {
			return i, false
		}
		// Convert mono to stereo by duplicating the sample to both channels
		samples[i][0] = r.samples[r.index]
		samples[i][1] = r.samples[r.index]
		r.index++
	}

	return len(samples), true
}

func (r *resampledStreamer) Err() error {
	return nil
}
