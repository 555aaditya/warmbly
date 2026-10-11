package models

import "time"

type MailTransportOperation string

type MailTransportObservationSource string

const (
	MailTransportClockUnknown MailTransportObservationSource = "unknown"
	MailTransportClockWorker  MailTransportObservationSource = "worker"
	MailTransportClockReceipt MailTransportObservationSource = "receipt"
)

const (
	MailTransportUnknown MailTransportOperation = "unknown"
	MailTransportSync    MailTransportOperation = "sync"
	MailTransportSend    MailTransportOperation = "send"
)

func (o MailTransportOperation) Known() bool {
	return o == MailTransportSync || o == MailTransportSend
}

func (o MailTransportOperation) Conservative() MailTransportOperation {
	if o.Known() {
		return o
	}
	return MailTransportUnknown
}

// Recovery needs an actual operation clock, not a synthesized receipt clock.
func ValidMailTransportTime(at, received time.Time) bool {
	return !at.IsZero() && at.Unix() > 0 && !at.After(received)
}
