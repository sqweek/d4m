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
	child := &DirNode{<-qidgen, name, node, nil}
	fmt.Println("created " + child.FullPath())
	node.children[name] = child
	return child
}

var root *DirNode

type SlashN struct {
	srv.Srv
}

func (sn *SlashN) Attach(req *srv.Req) {
	fmt.Println("attach")
	req.Fid.Aux = root
	req.RespondRattach(&root.qid)
}

func (sn *SlashN) Walk(req *srv.Req) {
	node := req.Fid.Aux.(*DirNode)
	if len(req.Tc.Wname) == 0 {
		req.Newfid.Aux = node
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
	req.Newfid.Aux = n
	req.RespondRwalk(qids)
}

func (sn *SlashN) Open(req *srv.Req) {
	dir := req.Fid.Aux.(*DirNode)
	req.RespondRopen(&dir.qid, 0)
}

func (sn *SlashN) Create(req *srv.Req) {
	if (req.Tc.Perm & p.DMDIR) == 0 {
		req.RespondError("permission denied")
		return
	}
	dir := req.Fid.Aux.(*DirNode)
	child := dir.Child(req.Tc.Name)
	req.RespondRcreate(&child.qid, 0)
}

func (sn *SlashN) Read(req *srv.Req) {
	node := req.Fid.Aux.(*DirNode)
	p.InitRread(req.Rc, req.Tc.Count)
	n := 0
	if req.Tc.Offset == 0 {
		b := req.Rc.Data
		for name := range node.children {
			d := p.Dir{Type: p.QTDIR, Qid: node.children[name].qid, Mode: p.DMDIR | 0755, Name: name}
			sz := p.PackDir(&d, b, req.Conn.Dotu)
			b = b[sz:]
			n += sz
		}
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
	dir := req.Fid.Aux.(*DirNode)
	if len(dir.children) != 0 || dir.parent == nil {
		req.RespondError("directory not empty")
		return
	}
	delete(dir.parent.children, dir.name)
	req.RespondRremove()
}

func (sn *SlashN) Stat(req *srv.Req) {
	dir, ok := req.Fid.Aux.(*DirNode)
	if !ok {
		req.RespondError("invalid request")
		return
	}
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

	root = &DirNode{<-qidgen, "", nil, nil}

	os.Remove("/tmp/ns.sqweek.:0/slashn")

	s := new(SlashN)
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