package chunk

// gear is the 256-entry random table used by the FastCDC rolling hash.
//
// It is derived from a fixed SplitMix64 seed instead of being a hand-pasted
// literal table. The table is part of the on-wire format: changing the seed
// changes every chunk boundary and would invalidate deduplication against
// existing repositories, so GearSeed must never change.
const GearSeed uint64 = 0x9E3779B97F4A7C15

var gear = buildGear(GearSeed)

func buildGear(seed uint64) [256]uint64 {
	var t [256]uint64
	s := seed
	for i := range t {
		s += 0x9E3779B97F4A7C15
		z := s
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		t[i] = z ^ (z >> 31)
	}
	return t
}
