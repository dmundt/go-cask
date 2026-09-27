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
	// Host and Start identify the process itself, which is the one thing the fields above
	// cannot say: whether the holder is still running. They are appended to the record, so a
	// five-field record written before them still parses — and a record with no host is never
	// read as dead, because a slot may be held by a clone on another machine or by a toolchain
	// that cannot name its host at all.
	Host  string
	Start string
}

// NoneToken is the token a record that carries none is read as. A holder without a
// token is a slot written by something that does not know about per-worktree tokens,
// which is never "this worktree holds it".
const NoneToken = "none"

// Fields renders the record the slot holds: seven tab-separated fields. The last two are
// appended, so the five-field form a previous build wrote stays readable.
func (h Holder) Fields() string {
	return strings.Join([]string{
		h.PID, strconv.FormatInt(h.Since, 10), h.Label, h.Who, h.Token, h.Host, h.Start,
	}, "\t")
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
		case 5:
			holder.Host = field
		case 6:
			holder.Start = field
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
	// Dead is an eviction of a slot whose holder is provably gone on this host and has been
	// idle past the grace period. It is its own reason because the holder never chose to let
	// go: the holder's next `renew` and `status` have to be able to say that, rather than
	// report an ordinary expiry.
	Dead = "dead"
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

// UnknownHost is the host a record carries when its writer could not name one. It is not a
// host name, so it never compares equal to this host, and a holder written under it is only
// ever Unknown rather than dead.
const UnknownHost = "unknown"

// Liveness is what the record and this host can say about the holder's process.
type Liveness int

const (
	// Alive: the host is this one and the same process is still running under that pid.
	Alive Liveness = iota
	// Gone: the host is this one and either nothing runs under that pid, or a different
	// process does — the pid was recycled, which is why the start time is recorded at all.
	Gone
	// Unknown: nothing can be said. The record names no host, or another one; the pid is not
	// a number; or this toolchain cannot read process start times. An unknown holder is never
	// taken over: absence of evidence is not evidence.
	Unknown
)

// LivenessOf judges the holder's process from its record and this host.
//
// processStart reports the start time of a running pid, and its ok result says whether this
// toolchain can read one at all. The distinction is the whole point: "this host can tell, and
// there is no such process" is Gone, while "this host cannot tell" is Unknown, and only Gone
// may ever take a slot over.
func LivenessOf(h Holder, thisHost string, processStart func(pid string) (string, bool)) Liveness {
	if h.Host == "" || h.Host == UnknownHost || h.Host != thisHost {
		return Unknown
	}
	pid, err := strconv.Atoi(h.PID)
	if err != nil || pid <= 0 {
		return Unknown
	}
	start, ok := processStart(h.PID)
	if !ok {
		return Unknown
	}
	if start == "" {
		return Gone
	}
	if h.Start != "" && start != h.Start {
		return Gone
	}
	return Alive
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
	// TakeoverDead: --takeover-dead takes a slot whose holder is provably gone on this host
	// and has been idle past the grace period. It is the one takeover that needs no idle
	// window: a process that no longer exists cannot renew, so waiting the window out would
	// hold every waiter for nothing.
	TakeoverDead
	// Unreadable: the slot exists but holds no holder yet, which is a claim in
	// progress. It is never taken over: the winner creates the slot before it writes
	// the record, and taking that half-written slot is how two acquirers end up
	// holding one lane.
	Unreadable
)

// TakesOver reports whether the outcome evicts the holder it found.
func (o Outcome) TakesOver() bool {
	return o == TakeoverExpired || o == TakeoverForced || o == TakeoverDead
}

// Decision is what an acquisition must do: the outcome, and the holder it found.
type Decision struct {
	Outcome Outcome
	Found   Holder
}

// Decide returns what the acquisition identified by who may do with the slot, with
// this worktree's outstanding token (mine, or NoneToken when it holds none), the
// caller's idle window, how long a provably gone holder must have been idle before it may be
// taken over (grace), and what this host can say about the holder's process.
//
// The refusals are the whole point of the function. A slot held by this identity
// through ANOTHER acquisition is not this session's to take, because nothing in the
// worktree distinguishes the two sessions; and a slot inside its idle window is a live
// landing, which the next session may only take with --force or, when the holder is provably
// gone and grace has passed, with takeDead.
func Decide(slot *Holder, who, mine string, window, grace time.Duration, force, takeDead bool, live Liveness, now int64) Decision {
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
	case force && slot.Idle(now) < window:
		decision.Outcome = TakeoverForced
	case takeDead && live == Gone && slot.Idle(now) >= grace:
		decision.Outcome = TakeoverDead
	case !force && slot.Idle(now) < window:
		decision.Outcome = RefusedFresh
	default:
		decision.Outcome = TakeoverExpired
	}
	return decision
}

// Evictable reports whether the slot's holder is provably gone on this host and has been idle
// past the grace period — the one state a caller may take over without waiting the window out,
// and the one `status` reports as its own rather than as a plain "someone else's".
func Evictable(slot *Holder, grace time.Duration, live Liveness, now int64) bool {
	return slot != nil && slot.Known() && live == Gone && slot.Idle(now) >= grace
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
