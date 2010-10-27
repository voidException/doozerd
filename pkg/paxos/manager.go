package paxos

import (
	"log"
	"os"

	"junta/store"
	"junta/util"
	"time"
)

const (
	fillDelay = 5e8 // 500ms
)

type result struct {
	seqn uint64
	v    string
}

type instReq struct {
	seqn uint64
	ch   chan *instance
}

type Manager struct {
	st      *store.Store
	rg      *Registrar
	learned chan result
	seqns   chan uint64
	fillUntil chan uint64
	reqs    chan instReq
	logger  *log.Logger
	Self    string
	alpha   int
	outs    PutterTo
}

// start is the seqn at which this member was defined.
// start+alpha is the first seqn this manager is expected to participate in.
func NewManager(self string, start uint64, alpha int, st *store.Store, outs PutterTo) *Manager {
	m := &Manager{
		st:      st,
		rg:      NewRegistrar(st, start, alpha),
		learned: make(chan result),
		seqns:   make(chan uint64),
		fillUntil:make(chan uint64),
		reqs:    make(chan instReq),
		logger:  util.NewLogger("manager"),
		Self:    self,
		alpha:   alpha,
		outs:    outs,
	}

	go m.gen(start+uint64(alpha))
	go m.fill(start+uint64(alpha))
	go m.process()

	return m
}

func (m *Manager) Alpha() int {
	return m.alpha
}

func (m *Manager) cluster(seqn uint64) *cluster {
	members, cals := m.rg.setsForSeqn(seqn)
	return newCluster(m.Self, members, cals, putToWrapper{seqn, m.outs})
}

func (mg *Manager) gen(next uint64) {
	for {
		cx := mg.cluster(next)
		leader := int(next % uint64(cx.Len()))
		if leader == cx.SelfIndex() {
			mg.seqns <- next
		}
		next++
	}
}

func (mg *Manager) fill(seqn uint64) {
	for next := range mg.fillUntil {
		for seqn < next {
			go mg.fillOne(seqn)
			seqn++
		}
		seqn = next + 1 // no need to fill in our own seqn
	}
}

func (m *Manager) process() {
	instances := make(map[uint64]*instance)
	for req := range m.reqs {
		inst, ok := instances[req.seqn]
		if !ok {
			inst = newInstance(req.seqn, m, m.learned)
			instances[req.seqn] = inst
		}
		req.ch <- inst
	}
}

func (m *Manager) getInstance(seqn uint64) *instance {
	ch := make(chan *instance)
	m.reqs <- instReq{seqn, ch}
	return <-ch
}

func (m *Manager) PutFrom(addr string, msg Msg) {
	if !msg.Ok() {
		return
	}
	it := m.getInstance(msg.Seqn())
	it.PutFrom(addr, msg)
}

func (m *Manager) proposeAt(seqn uint64, v string) {
	m.getInstance(seqn).Propose(v)
	m.logger.Logf("paxos propose -> %d %q", seqn, v)
}

func (m *Manager) Propose(v string) (uint64, string, os.Error) {
	seqn := <-m.seqns
	ch := m.st.Wait(seqn)
	m.proposeAt(seqn, v)
	m.fillUntil <- seqn
	ev := <-ch
	return seqn, ev.Mut, ev.Err
}

func (m *Manager) fillOne(seqn uint64) {
	time.Sleep(fillDelay)
	// yes, we'll act as coordinator for a seqn we don't "own"
	// this is intentional, since we want to exersize this code all the time,
	// not just during a failure.
	m.proposeAt(seqn, store.Nop)
}

func (m *Manager) Recv() (uint64, string) {
	result := <-m.learned
	m.logger.Logf("paxos %d learned <- %q", result.seqn, result.v)
	return result.seqn, result.v
}
