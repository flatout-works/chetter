package nats

import (
	"errors"
	"testing"
)

func TestIsConsumerConfigMismatch(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "max ack pending mismatch",
			err:  errors.New("nats: configuration requests max ack pending to be 4, but consumer's value is 1000"),
			want: true,
		},
		{
			name: "other nats error",
			err:  errors.New("nats: no responders available for request"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isConsumerConfigMismatch(tt.err); got != tt.want {
				t.Fatalf("isConsumerConfigMismatch() = %v, want %v", got, tt.want)
			}
		})
	}
}
