package cmdlib

func convertToStatusSet(xs map[string]StatusKind) map[string]bool {
	result := map[string]bool{}
	for k := range xs {
		result[k] = true
	}
	return result
}
