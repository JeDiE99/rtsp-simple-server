package recorder

import (
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediamtx/internal/logger"
)

const (
	// this corresponds to concatenationTolerance
	maxBasetime = 1 * time.Second
)

func findOldestNextSample(tracks []*formatFMP4Track) (*sample, time.Duration) {
	var oldestSample *sample
	var oldestDTS time.Duration

	for _, track := range tracks {
		if track.nextSample != nil {
			normalizedDTS := timestampToDuration(track.nextSample.dts, int(track.initTrack.TimeScale))
			if oldestSample == nil || normalizedDTS < oldestDTS {
				oldestSample = track.nextSample
				oldestDTS = normalizedDTS
			}
		}
	}

	return oldestSample, oldestDTS
}

type formatFMP4Track struct {
	f         *formatFMP4
	initTrack *fmp4.InitTrack

	nextSample *sample
}

func (t *formatFMP4Track) write(sample *sample) error {
	// wait the first video sample before setting hasVideo
	if t.initTrack.Codec.IsVideo() {
		t.f.hasVideo = true
	}

	sample, t.nextSample = t.nextSample, sample
	if sample == nil {
		return nil
	}
	sample.Duration = uint32(t.nextSample.dts - sample.dts)

	dtsDuration := timestampToDuration(sample.dts, int(t.initTrack.TimeScale))

	if t.f.currentSegment == nil {
		t.f.currentSegment = &formatFMP4Segment{
			f:        t.f,
			startDTS: dtsDuration,
			startNTP: sample.ntp,
		}
		t.f.currentSegment.initialize()
	} else if (dtsDuration - t.f.currentSegment.startDTS) < 0 { // BaseTime is negative, this is not supported by fMP4
		t.f.ri.Log(logger.Warn, "sample of track %d received too late, discarding", t.initTrack.ID)
		return nil
	}

	err := t.f.currentSegment.write(t, sample, dtsDuration)
	if err != nil {
		return err
	}

	nextDTSDuration := timestampToDuration(t.nextSample.dts, int(t.initTrack.TimeScale))

	if (!t.f.hasVideo || t.initTrack.Codec.IsVideo()) &&
		!t.nextSample.IsNonSyncSample &&
		(nextDTSDuration-t.f.currentSegment.startDTS) >= t.f.ri.segmentDuration {
		t.f.currentSegment.lastDTS = nextDTSDuration
		err := t.f.currentSegment.close()
		if err != nil {
			return err
		}

		// start next segment from the oldest next sample, in order to avoid the "negative basetime" issue
		oldestSample, oldestDTS := findOldestNextSample(t.f.tracks)

		// prevent going too back in time
		if (nextDTSDuration - oldestDTS) > maxBasetime {
			oldestSample = t.nextSample
			oldestDTS = nextDTSDuration
		}

		t.f.currentSegment = &formatFMP4Segment{
			f:        t.f,
			startDTS: oldestDTS,
			startNTP: oldestSample.ntp,
		}
		t.f.currentSegment.initialize()
	}

	return nil
}
