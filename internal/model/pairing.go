package model

import "time"

// PairingCode is an invitation to enrol one machine as an agent.
//
// It exists because the alternative is worse: an agent token is forty random
// characters, and getting one onto another computer means copying it through
// whatever channel happens to be to hand. A pairing code is short enough to
// read down a phone line or type into an installer prompt, and it is worth
// almost nothing if it leaks — it is good for one machine, once, for a few
// minutes, and it can be cancelled from the Hardware page in the meantime.
//
// Only the code's hash is kept, exactly as an agent token's is. The code
// itself is shown once, when it is minted, and GWatch cannot show it again.
type PairingCode struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`   // the name the machine will be registered under
	NodeID    *int64    `json:"nodeId"` // node the machine's readings will belong to
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	// RedeemedAt, RevokedAt and AgentID record how the code ended. A redeemed
	// code keeps its row so the Hardware page can say which machine came in on
	// which invitation, which is the only audit trail an enrolment leaves
	// besides the event log.
	RedeemedAt   *time.Time `json:"redeemedAt,omitempty"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	AgentID      *int64     `json:"agentId,omitempty"`
	RedeemedAddr string     `json:"redeemedAddr,omitempty"`
}

// Redeemed reports whether the code has already enrolled a machine.
func (p PairingCode) Redeemed() bool { return p.RedeemedAt != nil }

// Revoked reports whether an administrator cancelled the code.
func (p PairingCode) Revoked() bool { return p.RevokedAt != nil }

// Expired reports whether the code has run out of time.
func (p PairingCode) Expired(now time.Time) bool { return !now.Before(p.ExpiresAt) }

// Pending reports whether the code could still be redeemed. It is the one
// question the UI asks; the three ways of answering no are deliberately not
// distinguished to whoever presents a code, only to the administrator looking
// at the list.
func (p PairingCode) Pending(now time.Time) bool {
	return !p.Redeemed() && !p.Revoked() && !p.Expired(now)
}
