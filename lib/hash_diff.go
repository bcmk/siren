package lib

// HashDiffAll returns symmetric differenct between before and after
func HashDiffAll(before, after map[string]bool) []string {
	var changes []string
	for k := range after {
		if _, ok := before[k]; !ok {
			changes = append(changes, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			changes = append(changes, k)
		}
	}
	return changes
}

// HashDiffNewRemoved returns new and removed elements
func HashDiffNewRemoved(before, after map[string]bool) (new []string, removed []string) {
	for k := range after {
		if _, ok := before[k]; !ok {
			new = append(new, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			removed = append(removed, k)
		}
	}
	return
}
