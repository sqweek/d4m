package main

import (
	"code.google.com/p/go9p/p"
	"code.google.com/p/go9p/p/srv"
	"flag"
	"fmt"
	"os"
)

var maxDepth = flag.Int("depth", 2, "maximum directory depth")

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

func (node *DirNode) Child(name string) *DirNode {
	if node.children == nil {
		node.children = make(map[string]*DirNode)
	} else if child, ok := node.children[name]; ok {
		return child
	}
	child := &DirNode{<-qidgen, name, node, nil, 0}
	node.children[name] = child
	return child
}

type FidAux struct {
	node *DirNode
	readbuf []*DirNode
}

func NewFidAux(node *DirNode) *FidAux {
	aux := FidAux{node, nil}
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

	if node.Depth() + len(req.Tc.Wname) > *maxDepth {
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
	aux := GetAux(req.Fid)
	dir := aux.node
	req.RespondRopen(&dir.qid, 0)
}

func (sn *SlashN) Create(req *srv.Req) {
	if (req.Tc.Perm & p.DMDIR) == 0 {
		req.RespondError("permission denied")
		return
	}
	aux := GetAux(req.Fid)
	dir := aux.node
	child := dir.Child(req.Tc.Name)
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
		child := aux.readbuf[0]
		d := p.Dir{Type: p.QTDIR, Qid: child.qid, Mode: p.DMDIR | 0755, Name: child.name}
		sz := p.PackDir(&d, b, req.Conn.Dotu)
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

func (sn *SlashN) Write(req *srv.Req) {
	req.RespondError("can't write to /n")
}

func (sn *SlashN) Clunk(req *srv.Req) {
	req.RespondRclunk()
}

func (sn *SlashN) Remove(req *srv.Req) {
	aux := GetAux(req.Fid)
	dir := aux.node
	if len(dir.children) != 0 || dir.parent == nil {
		req.RespondError("directory not empty")
		return
	}
	delete(dir.parent.children, dir.name)
	req.RespondRremove()
}

func (sn *SlashN) Stat(req *srv.Req) {
	aux := GetAux(req.Fid)
	dir := aux.node
	req.RespondRstat(&p.Dir{Qid: dir.qid, Type: p.QTDIR, Name: dir.name, Mode: p.DMDIR | 0755})
}

func (sn *SlashN) Wstat(req *srv.Req) {
	req.RespondError("can't wstat /n")
}


var net = flag.String("net", "unix", "network type")
var addr = flag.String("addr", "/tmp/ns.sqweek.:0/slashn", "network address")

func main() {
	//uid := p.OsUsers.Uid2User(os.Geteuid())
	//gid := p.OsUsers.Gid2Group(os.Getegid())
	flag.Parse()

	go count(qidgen)

	os.Remove("/tmp/ns.sqweek.:0/slashn")

	s := new(SlashN)
	s.root = &DirNode{<-qidgen, "", nil, nil, 1}
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