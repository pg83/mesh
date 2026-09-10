package main

const windowBits = 1024

type Window struct {
	top  uint64
	bits [windowBits / 64]uint64
}

func (w *Window) bit(counter uint64) (int, uint64) {
	idx := counter % windowBits

	return int(idx / 64), 1 << (idx % 64)
}

func (w *Window) accept(counter uint64) bool {
	if counter > w.top {
		shift := counter - w.top

		if shift >= windowBits {
			w.bits = [windowBits / 64]uint64{}
		} else {
			for c := w.top + 1; c < counter; c++ {
				word, mask := w.bit(c)

				w.bits[word] &^= mask
			}
		}

		word, mask := w.bit(counter)

		w.bits[word] |= mask
		w.top = counter

		return true
	}

	if w.top-counter >= windowBits {
		return false
	}

	word, mask := w.bit(counter)

	if w.bits[word]&mask != 0 {
		return false
	}

	w.bits[word] |= mask

	return true
}
