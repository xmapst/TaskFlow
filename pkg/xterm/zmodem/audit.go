package zmodem

import (
	"log/slog"
	"time"
)

// Auditor authorizes FileOffered before forwarding the offer. False or an
// error rejects it. For other outcomes only the error is used (for logging).
// Calls are synchronous, outside the stream lock, and may be concurrent.
type Auditor func(FileAuditEvent) (allow bool, err error)

// FileAuditEvent describes one CRC-validated ZFILE offer and its observed
// outcome. Filename and DeclaredSize are sender claims, not filesystem facts.
// Acknowledged means the receiver sent ZRINIT after the sender's ZEOF, not
// that the destination was persisted or that a browser saved the download.
type FileAuditEvent struct {
	TransferID   uint64
	Direction    string
	Filename     string
	DeclaredSize int64
	OfferedAt    time.Time
	Outcome      FileOutcome
	Reason       string
}

type FileOutcome string

const (
	FileOffered      FileOutcome = "offered"
	FileRejected     FileOutcome = "rejected"
	FileAcknowledged FileOutcome = "acknowledged"
	FileSkipped      FileOutcome = "skipped"
	FileAborted      FileOutcome = "aborted"
	FileFailed       FileOutcome = "failed"
	FileIncomplete   FileOutcome = "incomplete"
)

// fileAudit runs under Stream.mu. Only the active offer is retained; identical
// re-offers before a terminal outcome are treated as retransmissions.
type fileAudit struct {
	pending                   *FileAuditEvent
	offerValid, accepted, eof bool
	client                    ZSession
	events                    []FileAuditEvent
}

func (z *Stream) startFileAudit() {
	if z.auditSink == nil {
		return
	}
	z.gate = &offerGate{parser: ZSession{Type: z.direction}}
	z.audit.client = ZSession{
		Type: z.direction, allowEarlyFinish: true, stopAtFinish: true,
		zOnHeader: func(h *ZmodemHeader) { z.auditHeader(z.direction == TypeUpload, h.Type) },
		ZFileHeaderCallback: func(info *ZFileInfo) {
			if z.direction == TypeUpload {
				z.auditOffer(info)
			}
		},
	}
}

func (z *Stream) auditOffer(info *ZFileInfo) {
	a := &z.audit
	if a.pending != nil && (a.pending.Filename != info.Filename() || a.pending.DeclaredSize != info.Size()) {
		z.finishFileAudit(FileIncomplete, "superseded_by_offer")
	}
	if a.pending == nil {
		a.pending = &FileAuditEvent{
			TransferID: z.id, Direction: z.direction, Filename: info.Filename(),
			DeclaredSize: info.Size(), OfferedAt: info.Time(),
		}
	}
	a.offerValid = true
}

func (z *Stream) auditHeader(fromSender bool, frame byte) {
	a := &z.audit
	switch frame {
	case ZABORT, ZCAN:
		z.finishFileAudit(FileAborted, "abort_header")
	case ZFERR:
		z.finishFileAudit(FileFailed, "file_error")
	case ZFIN:
		z.finishFileAudit(FileIncomplete, "finished_without_ack")
	case ZFILE:
		if fromSender {
			// Do not attribute a reply to the previous offer if the new
			// metadata never passes validation.
			a.offerValid, a.accepted, a.eof = false, false, false
		}
	}
	if a.pending == nil || !a.offerValid {
		return
	}
	if fromSender {
		switch frame {
		case ZDATA:
			a.eof = false
		case ZEOF:
			a.eof = a.accepted
		}
		return
	}
	switch frame {
	case ZRPOS:
		a.accepted, a.eof = true, false
	case ZNAK:
		a.eof = false
	case ZRINIT:
		if a.eof {
			z.finishFileAudit(FileAcknowledged, "receiver_ready_after_eof")
		}
	case ZSKIP:
		z.finishFileAudit(FileSkipped, "receiver_skipped")
	}
}

func (z *Stream) finishFileAudit(outcome FileOutcome, reason string) {
	a := &z.audit
	if a.pending != nil {
		a.pending.Outcome, a.pending.Reason = outcome, reason
		a.events = append(a.events, *a.pending)
		a.pending = nil
	}
	a.offerValid, a.accepted, a.eof = false, false, false
}

// filterOffer is called with mu held. Only the sender pump uses its gate.
func (z *Stream) filterOffer(data []byte) ([]byte, error) {
	gate := z.gate
	wire, err := gate.filter(data, func(info *ZFileInfo) error {
		event := FileAuditEvent{
			TransferID: z.id, Direction: z.direction, Filename: info.Filename(),
			DeclaredSize: info.Size(), OfferedAt: info.Time(), Outcome: FileOffered,
		}
		allow, err := func() (bool, error) {
			z.mu.Unlock()
			defer z.mu.Lock()
			return z.auditSink(event)
		}()
		if err == nil && allow && z.gate == gate && !z.closed && z.blocked == nil {
			return nil
		}
		event.Outcome, event.Reason = FileRejected, "auditor_denied"
		if err != nil {
			event.Reason = "auditor_error"
			slog.Warn("ZMODEM file authorization failed", slog.Uint64("transfer_id", event.TransferID), slog.Any("error", err))
		} else if allow {
			event.Reason = "transfer_ended_during_authorization"
		}
		z.audit.events = append(z.audit.events, event)
		return invalidOffer(event.Reason)
	})
	if err != nil {
		z.blocked = err
	}
	return wire, err
}

// Unlock before calling the external sink. Events from each individual
// read are ordered; opposite-direction calls may deliver concurrently.
func (z *Stream) unlockAndPublish() {
	events := z.audit.events
	z.audit.events = nil
	sink := z.auditSink
	z.mu.Unlock()
	if sink == nil {
		return
	}
	for _, event := range events {
		if _, err := sink(event); err != nil {
			slog.Warn("ZMODEM file audit callback failed", slog.Uint64("transfer_id", event.TransferID),
				slog.String("direction", event.Direction), slog.String("outcome", string(event.Outcome)), slog.Any("error", err))
		}
	}
}
