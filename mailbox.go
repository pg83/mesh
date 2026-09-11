package main

type Mailbox[T any] struct {
	in  chan T
	out chan T
}

func newMailbox[T any](done <-chan struct{}) *Mailbox[T] {
	m := &Mailbox[T]{in: make(chan T), out: make(chan T)}

	go func() {
		var queue []T

		for {
			var output chan T
			var first T

			if len(queue) != 0 {
				output, first = m.out, queue[0]
			}

			select {
			case message := <-m.in:
				queue = append(queue, message)
			case output <- first:
				var zero T

				queue[0] = zero
				queue = queue[1:]

				if len(queue) == 0 {
					queue = nil
				}
			case <-done:
				return
			}
		}
	}()

	return m
}

func post[T any](in chan<- T, message T) {
	if in != nil {
		in <- message
	}
}
