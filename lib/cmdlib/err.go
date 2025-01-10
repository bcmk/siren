package cmdlib

// CheckErr panics on an error
func CheckErr(err error) {
	if err != nil {
		panic(err)
	}
}
