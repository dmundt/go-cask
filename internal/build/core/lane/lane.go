// Package lane owns the landing lane's records: who a slot says holds it, how long it
// has been idle, what an acquisition may do with a slot it did not write, and the
// toolchain-neutral identity a holder is recognised by.
//
// Two things are deliberately facts here rather than at the call site. The record
// formats are fixed, because a slot written by one toolchain has to be readable by the
// other one — on this project a landing is carried out by Windows Git for the push and
// by WSL for the gate. And staleness is IDLE time, the moment the holder last refreshed
// the slot, never the time since it was acquired: a gate run plus a push that outlasts
// the window is a live landing, not an abandoned slot.
package lane

import (
	"strconv"
	"strings"
	"time"
)

// PrimaryWorktree is the name the primary checkout is identified by; a linked worktree
// is named by the base name of its directory, which is a bare name and therefore
// spelled the same way by every toolchain.
const PrimaryWorktree = "primary"

// Identity builds the holder identity: the clone's id, then the worktree and the
// branch, joined so that no absolute path can appear. The clone's id is a random value
// written once into the shared git directory, which is what keeps one clone's slot from
// being recognised as another's — and a path would defeat the purpose, because the
// toolchains spell this repository's directory as `D:/x/...` and `/mnt/d/x/...`.
func Identity(repoID, worktree, branch string) string {
	return repoID + "#primary:" + worktree + "#" + branch
}

// WorktreeName returns the name a checkout is identified by: the constant for the
// primary checkout, and the directory's base name for a linked worktree.
func WorktreeName(primary bool, directoryBase string) string {
	if primary {
		return PrimaryWorktree
	}
	return directoryBase
}

// Holder is one occupant of the slot: the process that took it, the moment it last
// refreshed it (Unix seconds), the label it was taken under, the identity that took it
// and the token of that acquisition.
type Holder struct {
	PID   string
	Since int64
	Label string
	Who   string
	Token string
}

// NoneToken is the token a record that carries none is read as. A holder without a
// token is a slot written by something that does not know about per-worktree tokens,
// which is never "this worktree holds it".
const NoneToken = "none"

// Fields renders the record the slot holds: five tab-separated fields.
func (h Holder) Fields() string {
	return strings.Join([]string{h.PID, strconv.FormatInt(h.Since, 10), h.Label, h.Who, h.Token}, "\t")
}

// ParseHolder reads a slot record. A record that is partial or unreadable yields the
// zero holder with the defaults a reader reports ("?" for a missing label or identity,
// no token) rather than an error: a slot whose holder cannot be read is a slot to take
// over, not a reason to stop.
func ParseHolder(record string) Holder {
	holder := Holder{Label: "?", Who: "?", Token: NoneToken}
	fields := strings.Split(strings.TrimRight(record, "\r\n"), "\t")
	for i, field := range fields {
		// A field of a line-based record cannot carry a line ending: a record written by
		// the other toolchain arrives with a carriage return, and a field that is
		// nothing but one is no value at all. Reading it as text would let a record
		// render a token it cannot read back.
		field = strings.TrimRight(field, "\r\n")
		switch i {
		case 0:
			holder.PID = field
		case 1:
			holder.Since, _ = strconv.ParseInt(field, 10, 64)
		case 2:
			if field != "" {
				holder.Label = field
			}
		case 3:
			if field != "" {
				holder.Who = field
			}
		case 4:
			if field != "" {
				holder.Token = field
			}
		}
	}
	return holder
}

// Idle returns how long the slot has been idle: the time since the holder last
// refreshed it, never the time since it was acquired.
func (h Holder) Idle(now int64) time.Duration {
	seconds := now - h.Since
	if seconds < 0 {
		// A slot stamped in the future is a clock that moved, not an occupied lane;
		// reading it as idle keeps an acquisition possible instead of wedging it.
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// IdleMinutes renders the idle time the way the status line reports it.
func (h Holder) IdleMinutes(now int64) int64 {
	return int64(h.Idle(now).Minutes())
}

// Known reports whether a record carries a holder at all. A claim creates the slot
// before it writes the record into it, so a slot that exists and holds nobody is a claim
// in progress rather than an abandoned slot: an acquirer that finds one must wait for the
// record instead of taking the slot over, or two acquirers end up holding it.
func (h Holder) Known() bool {
	return h.PID != "" && h.Who != "" && h.Who != "?"
}

// Takeover is the record an eviction leaves behind, for the holder that was evicted:
// the holder it evicted, the moment it happened, and why.
type Takeover struct {
	Holder
	// How is why the slot changed hands: "expired" when the idle window had passed,
	// "forced" when --force took a slot that was still inside its window.
	How string
}

// How values a takeover records.
const (
	// Expired is an eviction of a slot whose idle window had passed.
	Expired = "expired"
	// Forced is an eviction by --force of a slot that was still inside its window.
	Forced = "forced"
)

// Fields renders the takeover record: the same four fields as a holder, with why the
// slot changed hands in place of the acquisition token.
func (t Takeover) Fields() string {
	return strings.Join([]string{t.PID, strconv.FormatInt(t.Since, 10), t.Label, t.Who, t.How}, "\t")
}

// ParseTakeover reads a takeover record.
func ParseTakeover(record string) Takeover {
	holder := ParseHolder(record)
	return Takeover{Holder: holder, How: holder.Token}
}

// Outcome is what an acquisition may do with the slot it found.
type Outcome int

const (
	// Created: no slot was there, so this acquisition may claim it.
	Created Outcome = iota
	// AlreadyMine: this worktree holds the slot through this very acquisition.
	AlreadyMine
	// RefusedSameIdentity: the slot is this identity's, but through a different
	// acquisition in the same worktree. Refreshing it is the holder's deliberate
	// call, never a side effect of asking, so a second session in one worktree is
	// told to wait instead of being handed the lane.
	RefusedSameIdentity
	// RefusedFresh: another holder's slot is still inside its idle window.
	RefusedFresh
	// TakeoverExpired: another holder's slot has been idle past the window.
	TakeoverExpired
	// TakeoverForced: --force takes another holder's slot whatever its idle time.
	TakeoverForced
	// Unreadable: the slot exists but holds no holder yet, which is a claim in
	// progress. It is never taken over: the winner creates the slot before it writes
	// the record, and taking that half-written slot is how two acquirers end up
	// holding one lane.
	Unreadable
)

// TakesOver reports whether the outcome evicts the holder it found.
func (o Outcome) TakesOver() bool {
	return o == TakeoverExpired || o == TakeoverForced
}

// Decision is what an acquisition must do: the outcome, and the holder it found.
type Decision struct {
	Outcome Outcome
	Found   Holder
}

// Decide returns what the acquisition identified by who may do with the slot, with
// this worktree's outstanding token (mine, or NoneToken when it holds none) and the
// caller's idle window. A nil slot is a free one.
//
// The two refusals are the whole point of the function. A slot held by this identity
// through ANOTHER acquisition is not this session's to take, because nothing in the
// worktree distinguishes the two sessions; and a slot inside its idle window is a live
// landing, which the next session may only take with --force.
func Decide(slot *Holder, who, mine string, window time.Duration, force bool, now int64) Decision {
	if slot == nil {
		return Decision{Outcome: Created}
	}
	decision := Decision{Found: *slot}
	switch {
	case !slot.Known():
		decision.Outcome = Unreadable
	case slot.Who == who && slot.Token != NoneToken && slot.Token == mine:
		decision.Outcome = AlreadyMine
	case slot.Who == who:
		decision.Outcome = RefusedSameIdentity
	case !force && slot.Idle(now) < window:
		decision.Outcome = RefusedFresh
	case force && slot.Idle(now) < window:
		decision.Outcome = TakeoverForced
	default:
		decision.Outcome = TakeoverExpired
	}
	return decision
}

// Status is what `status` reports about the slot.
type Status int

const (
	// Free: nobody holds the slot.
	Free Status = iota
	// Mine: this worktree holds the slot through an outstanding acquisition.
	Mine
	// Other: someone else holds it — another identity, or another acquisition in
	// this worktree.
	Other
)

// SlotStatus classifies a slot for the caller: free, its own, or someone else's.
//
// A slot that carries no readable holder is never this worktree's, which is what keeps
// `status` and `Decide` telling one story: an acquirer waits for a record still being
// written, and the holder of that record is not told it holds a slot it would then be
// refused on.
func SlotStatus(slot *Holder, who, mine string) Status {
	switch {
	case slot == nil:
		return Free
	case slot.Known() && slot.Who == who && slot.Token != NoneToken && slot.Token == mine:
		return Mine
	default:
		return Other
	}
}

// ExitCode is the status a shell caller reads: 0 held by this worktree, 1 free, 2 held
// by someone else. The three are distinct because a caller decides differently on each:
// proceed, take it, ask.
func (s Status) ExitCode() int {
	switch s {
	case Mine:
		return 0
	case Free:
		return 1
	default:
		return 2
	}
}
