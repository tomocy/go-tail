package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestRun(t *testing.T) {
	dst := new(bytes.Buffer)
	app := app{
		dst:      dst,
		openSrcs: fakeOpenSrcs(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(time.Millisecond)
		cancel()
	}()

	if err := app.run(ctx, []string{"/program", "a.txt", "b.txt"}); err != nil {
		t.Fatalf("should have run: %s", err)
	}

	assertContain(t, "output", dst.String(), "[a.txt] test a.txt content")
	assertContain(t, "output", dst.String(), "[b.txt] test b.txt content")
}

func TestFollowSrcs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	srcs := []src{
		{
			name: "test name 1",
			r:    strings.NewReader("test contents 1"),
		},
		{
			name: "test name 2",
			r:    strings.NewReader("test contents 2"),
		},
	}

	wg.Add(len(srcs))
	chs, err := followSrcs(ctx, &wg, srcs...)
	if err != nil {
		t.Fatalf("should have followed the given sources: %s", err)
	}

	expected := []data{
		{
			name:     "test name 1",
			contents: []byte("test contents 1"),
		},
		{
			name:     "test name 2",
			contents: []byte("test contents 2"),
		},
	}

	for i, ch := range chs {
		assertData(t, <-ch, expected[i])
	}

	cancel()

	assertFinishWaiting(&wg)
}

func TestFollowSrc(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	src := src{
		name: "test name",
		r:    strings.NewReader("test contents"),
	}

	wg.Add(1)
	ch, err := followSrc(ctx, &wg, src)
	if err != nil {
		t.Fatalf("should have followed the given source: %s", err)
	}

	actual := <-ch
	assertData(t, actual, data{
		name:     "test name",
		contents: []byte("test contents"),
	})

	cancel()

	assertFinishWaiting(&wg)
}

func TestSelectData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dataChs := []<-chan data{
		fakeDataCh(ctx, data{
			name:     "test name 1",
			contents: []byte("test content 1"),
		}),
		fakeDataCh(ctx, data{
			name:     "test name 2",
			contents: []byte("test content 2"),
		}),
	}

	var wg sync.WaitGroup

	dst := new(bytes.Buffer)

	wg.Add(1)
	go selectData(ctx, &wg, dst, dataChs...)

	time.Sleep(time.Millisecond)

	assertContain(t, "output", dst.String(), "[test name 1] test content 1")
	assertContain(t, "output", dst.String(), "[test name 2] test content 2")

	assertFinishWaiting(&wg)
}

func assertData(t *testing.T, actual, expected data) {
	assertEqual(t, "name", actual.name, expected.name)
	assertEqual(t, "contents", string(actual.contents), string(expected.contents))
}

func assertEqual(t *testing.T, name string, actual, expected interface{}) {
	if actual != expected {
		reportNotEqual(t, name, actual, expected)
	}
}

func assertContain(t *testing.T, name string, actual, expected string) {
	if !strings.Contains(actual, expected) {
		reportNotContain(t, name, actual, expected)
	}
}

func reportNotEqual(t *testing.T, name string, actual, expected interface{}) {
	t.Errorf("%s: expected %v to equal to %v", name, actual, expected)
}

func reportNotContain(t *testing.T, name, actual, expected string) {
	t.Errorf("%s: expected %q to contain %q", name, actual, expected)
}

func assertFinishWaiting(wg *sync.WaitGroup) error {
	ch := make(chan struct{})
	// This goroutine leaks in timeout
	go func() {
		defer close(ch)
		wg.Wait()
	}()

	select {
	case <-time.After(time.Second):
		return fmt.Errorf("timed out")
	case <-ch:
		return nil
	}
}

func fakeOpenSrcs() func(...string) ([]src, func(), error) {
	return func(names ...string) ([]src, func(), error) {
		srcs := make([]src, len(names))
		for i, name := range names {
			srcs[i] = src{
				name: name,
				r:    strings.NewReader(fmt.Sprintf("test %s content", name)),
			}
		}

		return srcs, func() {}, nil
	}
}

func fakeDataCh(ctx context.Context, d data) <-chan data {
	ch := make(chan data)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case ch <- d:
		}
	}()

	return ch
}
