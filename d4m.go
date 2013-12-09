package main

import (
	"code.google.com/p/go9p/p"
	"code.google.com/p/go9p/p/srv"
	"errors"
	"sync"
	"flag"
	"fmt"
	"os"
)

var qidgen = make(chan p.Qid)

func count(c chan p.Qid) {
	i := uint64(0)
	for {
		c <- p.Qid{p.QTDIR, 0, i}
		i++
		if (i == 0) {
			panic("qids exhausted!")
		}
	}
}

type DirNode struct {
	sync.Mutex
	qid p.Qid
	name string
	parent *DirNode
	children map[string]*DirNode
	refcount int //number of fids actively referencing this dir
}

func (node *DirNode) Depth() int {
	depth := 0
	for n := node; n.parent != nil; n = n.parent {
		depth++
	}
	return depth
}

func (node *DirNode) FullPath() string {
	s := node.name
	for n := node.parent; n != nil; n = n.parent {
		if n.name != "" {
			s = n.name + "/" + s
		}
	}
	return s
}

func NewDirNode(parent *DirNode, name string) *DirNode {
	node := DirNode{qid: <-qidgen, name: name, parent: parent}
	return &node
}

func (node *DirNode) Child(name string) *DirNode {
	if node.children == nil {
		node.children = make(map[string]*DirNode)
	} else if child, ok := node.children[name]; ok {
		return child
	}
	child := NewDirNode(node, name)
	node.children[name] = child
	return child
}

func (node *DirNode) Rmdir() error {
	if len(node.children) != 0 {
		return errors.New("directory not empty")
	}
	if node.parent == nil {
		return errors.New("can't remove root dir")
	}
	delete(node.parent.children, node.name)
	return nil
}

func (node *DirNode) Dir() *p.Dir {
	return &p.Dir{Qid: node.qid, Type: p.QTDIR, Name: node.name, Mode: p.DMDIR | 0755}
}

func (node *DirNode) IncRef() {
	node.Lock()
	node.refcount++
	node.Unlock()
}

func (node *DirNode) DecRef() {
	node.Lock()
	node.refcount--
	done := (node.refcount == 0)
	node.Unlock()
	if done {
		node.Rmdir()
	}
}

type FidAux struct {
	node *DirNode
	readbuf []*DirNode
}

func NewFidAux(node *DirNode) *FidAux {
	aux := FidAux{node, nil}
	node.IncRef()
	return &aux
}

func GetAux(fid *srv.Fid) *FidAux {
	aux, ok := fid.Aux.(*FidAux)
	if !ok {
		panic("wrong type on fid Aux")
	}
	return aux
}

type SlashN struct {
	srv.Srv
	root *DirNode
	maxDepth int
}

func (sn *SlashN) Attach(req *srv.Req) {
	req.Fid.Aux = NewFidAux(sn.root)
	req.RespondRattach(&sn.root.qid)
}

func (sn *SlashN) Walk(req *srv.Req) {
	aux := GetAux(req.Fid)
	node := aux.node
	if len(req.Tc.Wname) == 0 {
		req.Newfid.Aux = NewFidAux(node)
		req.RespondRwalk([]p.Qid{})
		return
	}

	if node.Depth() + len(req.Tc.Wname) > sn.maxDepth {
		req.RespondError("maximum depth exceeded")
		return
	}

	qids := make([]p.Qid, len(req.Tc.Wname))
	n := node
	for i := 0; i < len(req.Tc.Wname); i++ {
		n = n.Child(req.Tc.Wname[i])
		qids[i] = n.qid
	}
	req.Newfid.Aux = NewFidAux(n)
	req.RespondRwalk(qids)
}

func (sn *SlashN) Open(req *srv.Req) {
	req.RespondRopen(&GetAux(req.Fid).node.qid, 0)
}

func (sn *SlashN) Create(req *srv.Req) {
	if (req.Tc.Perm & p.DMDIR) == 0 {
		req.RespondError("permission denied")
		return
	}
	child := GetAux(req.Fid).node.Child(req.Tc.Name)
	req.RespondRcreate(&child.qid, 0)
}

func (sn *SlashN) Read(req *srv.Req) {
	aux := GetAux(req.Fid)
	node := aux.node
	p.InitRread(req.Rc, req.Tc.Count)
	if req.Tc.Offset == 0 {
		aux.readbuf = make([]*DirNode, len(node.children))
		i := 0
		for _, child := range node.children {
			aux.readbuf[i] = child
			i++
		}
	}
	n := 0
	b := req.Rc.Data
	for len(aux.readbuf) > 0 {
		sz := p.PackDir(aux.readbuf[0].Dir(), b, req.Conn.Dotu)
		if sz == 0 {
			break
		}
		b = b[sz:]
		n += sz
		aux.readbuf = aux.readbuf[1:]
	}
	p.SetRreadCount(req.Rc, uint32(n))
	req.Respond()
}

func (sn *SlashN) Clunk(req *srv.Req) {
	GetAux(req.Fid).node.DecRef()
	req.RespondRclunk()
}

func (sn *SlashN) Stat(req *srv.Req) {
	req.RespondRstat(GetAux(req.Fid).node.Dir())
}

func (sn *SlashN) Write(req *srv.Req) {
	req.RespondError("permission denied")
}

func (sn *SlashN) Remove(req *srv.Req) {
	req.RespondError("permission denied")
}

func (sn *SlashN) Wstat(req *srv.Req) {
	req.RespondError("permission denied")
}


var net = flag.String("net", "unix", "network type")
var addr = flag.String("addr", "/tmp/ns.sqweek.:0/slashn", "network address")
var maxDepth = flag.Int("depth", 2, "maximum directory depth")

func main() {
	//uid := p.OsUsers.Uid2User(os.Geteuid())
	//gid := p.OsUsers.Gid2Group(os.Getegid())
	flag.Parse()

	go count(qidgen)

	os.Remove("/tmp/ns.sqweek.:0/slashn")

	s := new(SlashN)
	s.root = NewDirNode(nil, "")
	s.maxDepth = *maxDepth
	s.Id = "/n"
	s.Debuglevel = srv.DbgPrintFcalls
	s.Dotu = false
	
	s.Start(s)
	err := s.StartNetListener(*net, *addr)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
	}

	return
}