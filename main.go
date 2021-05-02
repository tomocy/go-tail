package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
)

func main() {
	app := app{
		dst:      os.Stdout,
		openSrcs: openFiles,
	}

	if err := app.run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type app struct {
	dst      io.Writer
	openSrcs func(...string) ([]src, func(), error)
}

func (a app) run(ctx context.Context, args []string) error {
	defer log.Println("finished running")

	names := args[1:]

	if len(names) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup

	srcs, close, err := a.openSrcs(names...)
	defer close()

	wg.Add(len(names))
	chs, err := followSrcs(ctx, &wg, srcs...)
	if err != nil {
		return fmt.Errorf("failed to follow files: %w", err)
	}

	wg.Add(1)
	go selectData(ctx, &wg, a.dst, chs...)

	sigs := notifyOnSignals(syscall.SIGINT)

	select {
	case <-ctx.Done():
	case <-sigs:
		cancel()
	}

	wg.Wait()

	return nil
}

func openFiles(names ...string) ([]src, func(), error) {
	srcs, files := make([]src, len(names)), make([]*os.File, len(names))

	for i, name := range names {
		f, err := os.Open(name)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open %s: %w", name, err)
		}

		files[i] = f

		srcs[i] = src{
			name: name,
			r:    f,
		}
	}

	return srcs, func() {
		for _, f := range files {
			f.Close()
		}
	}, nil
}

func followSrcs(ctx context.Context, wg *sync.WaitGroup, srcs ...src) ([]<-chan data, error) {
	chs := make([]<-chan data, len(srcs))
	for i, src := range srcs {
		ch, err := followSrc(ctx, wg, src)
		if err != nil {
			return nil, fmt.Errorf("failed to follow %s: %w", src.name, err)
		}

		chs[i] = ch
	}

	return chs, nil
}

func followSrc(ctx context.Context, wg *sync.WaitGroup, src src) (<-chan data, error) {
	ch := make(chan data)

	go func() {
		defer log.Printf("finished following %s", src.name)
		defer wg.Done()
		defer close(ch)

		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if n, _ := src.r.Read(buf); n > 0 {
					ch <- data{
						name:     src.name,
						contents: buf[:n],
					}
				}
			}
		}
	}()

	return ch, nil
}

func selectData(ctx context.Context, wg *sync.WaitGroup, dst io.Writer, chs ...<-chan data) {
	defer log.Println("finished selecting data")
	defer wg.Done()

	rCases := make([]reflect.SelectCase, len(chs))
	for i, ch := range chs {
		rCases[i] = reflect.SelectCase{
			Chan: reflect.ValueOf(ch),
			Dir:  reflect.SelectRecv,
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, received, ok := reflect.Select(rCases)
			if !ok {
				continue
			}
			file, ok := received.Interface().(data)
			if !ok {
				continue
			}
			fmt.Fprintf(dst, "[%s] %s\n", file.name, file.contents)
		}
	}
}

func notifyOnSignals(signals ...os.Signal) <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT)
	return ch
}

type src struct {
	name string
	r    io.Reader
}

type data struct {
	name     string
	contents []byte
}
