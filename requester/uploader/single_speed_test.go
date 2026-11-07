package uploader

import (
	"bytes"
	"io"
	"math"
	"testing"
	"time"
)

func TestSingleUploadSpeedIncludesBytesBeforeSubscription(t *testing.T) {
	u := NewUploader("fixture://upload/", resumeReader{bytes.NewReader(make([]byte, 1000))})
	u.begin()
	u.executeTime = time.Now().Add(-10 * time.Second)
	if _, err := io.CopyN(io.Discard, u.readed64, 1000); err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	status := make(chan Status)
	done := make(chan struct{})
	go func() { defer close(done); u.streamStatus(status, ticks) }()
	defer func() { close(u.finished); <-done }()
	ticks <- time.Now()
	p := <-status
	if p.Uploaded() != 1000 || p.SpeedsPerSecond() < 90 || p.SpeedsPerSecond() > 100 {
		t.Fatalf("late subscriber snapshot: uploaded=%d rate=%d, want 1000 bytes at about 100 B/s", p.Uploaded(), p.SpeedsPerSecond())
	}
}

func TestSingleUploadSpeedUsesSampleInterval(t *testing.T) {
	u := NewUploader("fixture://upload/", resumeReader{bytes.NewReader(make([]byte, 7000))})
	u.begin()
	ticks := make(chan time.Time)
	status := make(chan Status)
	done := make(chan struct{})
	var sampleTime time.Time
	go func() { defer close(done); u.streamStatusAt(status, ticks, func() time.Time { return sampleTime }) }()
	defer func() { close(u.finished); <-done }()
	for _, tc := range []struct {
		after                time.Duration
		read, uploaded, rate int64
	}{
		{time.Second, 1000, 1000, 1000},
		{5 * time.Second, 2000, 3000, 500},
		{5500 * time.Millisecond, 1000, 4000, 2000},
		{6500 * time.Millisecond, 0, 4000, 0},
		// A non-advancing clock must retain the unsampled bytes.
		{6500 * time.Millisecond, 1000, 5000, 0},
		{7500 * time.Millisecond, 2000, 7000, 3000},
	} {
		if _, err := io.CopyN(io.Discard, u.readed64, tc.read); err != nil {
			t.Fatal(err)
		}
		sampleTime = u.executeTime.Add(tc.after)
		// The ticker timestamp may be stale after a blocked status send. The
		// clock used for the byte snapshot must define the rate instead.
		ticks <- u.executeTime
		p := <-status
		if p.Uploaded() != tc.uploaded || p.TotalSize() != 7000 || p.SpeedsPerSecond() != tc.rate || p.TimeElapsed() != tc.after {
			t.Fatalf("at %v: uploaded=%d total=%d rate=%d elapsed=%v, want uploaded=%d rate=%d", tc.after, p.Uploaded(), p.TotalSize(), p.SpeedsPerSecond(), p.TimeElapsed(), tc.uploaded, tc.rate)
		}
	}
}

func TestSingleUploadSpeedWhileReceiverIsBlocked(t *testing.T) {
	u := NewUploader("fixture://upload/", resumeReader{bytes.NewReader(make([]byte, 3000))})
	u.begin()
	ticks := make(chan time.Time, 1)
	status := make(chan Status)
	sampled := make(chan struct{}, 2)
	sampleTimes := make(chan time.Time, 2)
	sampleTimes <- u.executeTime.Add(time.Second)
	sampleTimes <- u.executeTime.Add(5 * time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.streamStatusAt(status, ticks, func() time.Time {
			snapshotTime := <-sampleTimes
			sampled <- struct{}{}
			return snapshotTime
		})
	}()
	defer func() { close(u.finished); <-done }()
	if _, err := io.CopyN(io.Discard, u.readed64, 1000); err != nil {
		t.Fatal(err)
	}
	ticks <- u.executeTime
	<-sampled
	// The first snapshot cannot be delivered while the body keeps advancing.
	if _, err := io.CopyN(io.Discard, u.readed64, 2000); err != nil {
		t.Fatal(err)
	}
	ticks <- u.executeTime
	first, next := <-status, <-status
	if first.Uploaded() != 1000 || first.SpeedsPerSecond() != 1000 || next.Uploaded() != 3000 || next.SpeedsPerSecond() != 500 {
		t.Fatalf("blocked receiver: first=%+v next=%+v", first, next)
	}
}

func TestSingleUploadSpeedSaturatesInsteadOfOverflowing(t *testing.T) {
	u := NewUploader("fixture://upload/", resumeReader{bytes.NewReader(nil)})
	u.begin()
	u.readed64 = &fixedReadStatus{Readed64: u.readed64, count: math.MaxInt64}
	ticks := make(chan time.Time)
	status := make(chan Status)
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.streamStatusAt(status, ticks, func() time.Time { return u.executeTime.Add(time.Nanosecond) })
	}()
	defer func() { close(u.finished); <-done }()
	ticks <- time.Time{}
	if got := (<-status).SpeedsPerSecond(); got != math.MaxInt64 {
		t.Fatalf("extreme rate=%d, want saturation", got)
	}
}

type fixedReadStatus struct {
	Readed64
	count int64
}

func (s *fixedReadStatus) Readed() int64 { return s.count }
