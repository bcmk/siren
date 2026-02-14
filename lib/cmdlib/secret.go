package cmdlib

// Secret is a string type that redacts its value in JSON and logs.
type Secret string

// MarshalJSON redacts the secret value.
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"***"`), nil
}

// String redacts the secret value.
func (s Secret) String() string {
	return "***"
}
