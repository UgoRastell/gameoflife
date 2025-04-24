// server/main.go — plateau 500×500, N motifs aléatoires semés au démarrage
package main

import (
	"flag"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	pb "github.com/ugora/gameoflife/proto"
	"google.golang.org/grpc"
)

/*───── flags ────────────────────────────────────────────────────────────────*/

var (
	startInstances = flag.Int("n", 30, "nombre d'instances aléatoires à semer")
)

/*───── grille & pas de temps ───────────────────────────────────────────────*/

const (
	W, H = 500, 500
	dt   = 100 * time.Millisecond
)

/*───── motifs de base (8 familles) ─────────────────────────────────────────*/

var presets = map[int][][2]int32{
	1: {{1, 0}, {2, 1}, {0, 2}, {1, 2}, {2, 2}},                                 // Glider
	2: {{1, 0}, {2, 0}, {3, 0}, {4, 0}, {0, 1}, {4, 1}, {4, 2}, {0, 3}, {3, 3}}, // LWSS
	3: pulsar(),                                                                 // Pulsar
	4: {{0, 1}, {1, 1}, {2, 1}},                                                 // Blinker
	5: {{0, 0}, {1, 0}, {0, 1}, {1, 1}, {2, 2}, {3, 2}, {2, 3}, {3, 3}},         // Beacon
	6: {{1, 0}, {2, 0}, {0, 1}, {1, 1}, {1, 2}},                                 // R-pentomino
	7: {{6, 0}, {0, 1}, {1, 1}, {1, 2}, {5, 2}, {6, 2}, {7, 2}},                 // Diehard
	8: {{1, 0}, {3, 1}, {0, 2}, {1, 2}, {4, 2}, {5, 2}, {6, 2}},                 // Acorn
}

func pulsar() (out [][2]int32) {
	raw := [][]int{
		{2, 0}, {3, 0}, {4, 0}, {8, 0}, {9, 0}, {10, 0},
		{0, 2}, {5, 2}, {7, 2}, {12, 2}, {0, 3}, {5, 3}, {7, 3}, {12, 3},
		{0, 4}, {5, 4}, {7, 4}, {12, 4}, {2, 5}, {3, 5}, {4, 5}, {8, 5}, {9, 5}, {10, 5},
		{2, 7}, {3, 7}, {4, 7}, {8, 7}, {9, 7}, {10, 7}, {0, 8}, {5, 8}, {7, 8}, {12, 8},
		{0, 9}, {5, 9}, {7, 9}, {12, 9}, {0, 10}, {5, 10}, {7, 10}, {12, 10},
		{2, 12}, {3, 12}, {4, 12}, {8, 12}, {9, 12}, {10, 12},
	}
	for _, p := range raw {
		out = append(out, [2]int32{int32(p[0]), int32(p[1])})
	}
	return
}

/*───── fonctions utilitaires pour le “scatter” ─────────────────────────────*/

func bbox(p [][2]int32) (w, h int32) {
	for _, c := range p {
		if c[0] > w {
			w = c[0]
		}
		if c[1] > h {
			h = c[1]
		}
	}
	return w + 1, h + 1
}

func scatter(seed map[[2]int32]struct{}, pid int, n int) {
	pat := presets[pid]
	pw, ph := bbox(pat)
	for i := 0; i < n; i++ {
		ox := rand.Int31n(W - pw)
		oy := rand.Int31n(H - ph)
		for _, c := range pat {
			seed[[2]int32{c[0] + ox, c[1] + oy}] = struct{}{}
		}
	}
}

/*───── état global ─────────────────────────────────────────────────────────*/

var (
	board   = make(map[[2]int32]struct{})
	boardMu sync.RWMutex
	gen     int32
)

/*───── évolution du plateau ───────────────────────────────────────────────*/

func evolve() {
	next := make(map[[2]int32]struct{})
	neigh := map[[2]int32]int{}
	boardMu.RLock()
	for c := range board {
		x, y := c[0], c[1]
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				if dx == 0 && dy == 0 {
					continue
				}
				nx := (x + int32(dx) + W) % W
				ny := (y + int32(dy) + H) % H
				neigh[[2]int32{nx, ny}]++
			}
		}
	}
	for c, n := range neigh {
		_, alive := board[c]
		if n == 3 || (alive && n == 2) {
			next[c] = struct{}{}
		}
	}
	boardMu.RUnlock()
	boardMu.Lock()
	board = next
	gen++
	boardMu.Unlock()
}

/*───── hub clients (identique à la version précédente) ────────────────────*/

type client struct {
	vx, vy, vw, vh int32
	stream         pb.GameOfLife_StreamBoardsServer
	done           <-chan struct{}
}

type hub struct {
	mu sync.RWMutex
	cs map[*client]struct{}
}

func newHub() *hub              { return &hub{cs: map[*client]struct{}{}} }
func (h *hub) add(c *client)    { h.mu.Lock(); h.cs[c] = struct{}{}; h.mu.Unlock() }
func (h *hub) remove(c *client) { h.mu.Lock(); delete(h.cs, c); h.mu.Unlock() }

func (h *hub) broadcast() {
	boardMu.RLock()
	live := board
	g := gen
	boardMu.RUnlock()
	h.mu.RLock()
	for c := range h.cs {
		select {
		case <-c.done:
			h.mu.RUnlock()
			h.remove(c)
			h.mu.RLock()
			continue
		default:
		}
		var slice []*pb.Cell
		for cell := range live {
			if cell[0] >= c.vx && cell[0] < c.vx+c.vw &&
				cell[1] >= c.vy && cell[1] < c.vy+c.vh {
				slice = append(slice, &pb.Cell{X: cell[0] - c.vx, Y: cell[1] - c.vy})
			}
		}
		_ = c.stream.Send(&pb.Board{Generation: g, Alive: slice, SentUnixns: time.Now().UnixNano()})
	}
	h.mu.RUnlock()
}

/*───── service gRPC ───────────────────────────────────────────────────────*/

type lifeSrv struct {
	h *hub
	pb.UnimplementedGameOfLifeServer
}

func (s *lifeSrv) StreamBoards(req *pb.ViewportRequest,
	stream pb.GameOfLife_StreamBoardsServer) error {

	c := &client{
		vx: req.ViewX, vy: req.ViewY, vw: req.ViewW, vh: req.ViewH,
		stream: stream, done: stream.Context().Done(),
	}
	s.h.add(c)
	<-stream.Context().Done()
	s.h.remove(c)
	return nil
}

/*───── main ───────────────────────────────────────────────────────────────*/

func main() {
	flag.Parse()
	rand.Seed(time.Now().UnixNano())

	/*— sème N instances aléatoires —*/
	for i := 0; i < *startInstances; i++ {
		id := rand.Intn(len(presets)) + 1 // id 1-8
		scatter(board, id, 1)
	}

	/*— lance la boucle de vie —*/
	go func() {
		for t := time.NewTicker(dt); ; <-t.C {
			evolve()
		}
	}()

	/*— hub & diffusion —*/
	h := newHub()
	go func() {
		for t := time.NewTicker(dt); ; <-t.C {
			h.broadcast()
		}
	}()

	lis, _ := net.Listen("tcp", ":50051")
	grpcServer := grpc.NewServer()
	pb.RegisterGameOfLifeServer(grpcServer, &lifeSrv{h: h})
	log.Printf("server on :50051 — %d motifs semés", *startInstances)
	_ = grpcServer.Serve(lis)
}
