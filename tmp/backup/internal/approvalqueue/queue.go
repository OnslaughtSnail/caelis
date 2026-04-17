package approvalqueue

import (
	"context"
	"sync"
)

type Queue struct {
	once sync.Once

	mu    sync.Mutex
	cond  *sync.Cond
	tasks []task
}

type task struct {
	ctx  context.Context
	run  func(context.Context) error
	done chan error
}

func New() *Queue {
	q := &Queue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *Queue) Do(ctx context.Context, run func(context.Context) error) error {
	if q == nil {
		if run == nil {
			return nil
		}
		return run(ctx)
	}
	q.once.Do(func() {
		go q.loop()
	})
	done := make(chan error, 1)
	q.mu.Lock()
	q.tasks = append(q.tasks, task{
		ctx:  ctx,
		run:  run,
		done: done,
	})
	q.cond.Signal()
	q.mu.Unlock()
	return <-done
}

func (q *Queue) loop() {
	for {
		q.mu.Lock()
		for len(q.tasks) == 0 {
			q.cond.Wait()
		}
		next := q.tasks[0]
		q.tasks = q.tasks[1:]
		q.mu.Unlock()

		err := next.ctx.Err()
		if err == nil && next.run != nil {
			err = next.run(next.ctx)
		}
		next.done <- err
	}
}
