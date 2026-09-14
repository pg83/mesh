package main

const replayWindow = 2048

type Replay struct {
	max  uint64
	bits [replayWindow / 64]uint64
}

func (r *Replay) slot(id uint64) (int, uint64) {
	position := id % replayWindow

	return int(position / 64), uint64(1) << (position % 64)
}

func (r *Replay) accept(id uint64) bool {
	if id > r.max {
		if id-r.max >= replayWindow {
			r.bits = [replayWindow / 64]uint64{}
		} else {
			for next := r.max + 1; next <= id; next++ {
				word, bit := r.slot(next)

				r.bits[word] &^= bit
			}
		}

		r.max = id
	} else if r.max-id >= replayWindow {
		return false
	}

	word, bit := r.slot(id)

	if r.bits[word]&bit != 0 {
		return false
	}

	r.bits[word] |= bit

	return true
}
