// main.go – serveur gRPC Game of Life
// ----------------------------------
// Lance un service gRPC "GameOfLife" (défini dans proto/life.proto) qui
// streame les générations du jeu de la vie.
//
// Build :
//   go run ./server
//
// Usage côté client : voir README ou front CLI/web.

package main

import (
	"log"
	"net"
	"time"

	"google.golang.org/grpc"

	pb "github.com/ugora/gameoflife/proto"
)

// Board représente une grille torique.
type Board struct {
	width, height int32
	alive         map[[2]int32]struct{}
}

// Next calcule la génération suivante.
func (b *Board) Next() *Board {
	next := &Board{width: b.width, height: b.height, alive: make(map[[2]int32]struct{})}
	neighborCount := make(map[[2]int32]int32)

	for coord := range b.alive {
		x, y := coord[0], coord[1]
		for dx := int32(-1); dx <= 1; dx++ {
			for dy := int32(-1); dy <= 1; dy++ {
				if dx == 0 && dy == 0 {
					continue
				}
				nx := (x + dx + b.width) % b.width
				ny := (y + dy + b.height) % b.height
				neighborCount[[2]int32{nx, ny}]++
			}
		}
	}

	for coord, n := range neighborCount {
		_, alive := b.alive[coord]
		if n == 3 || (alive && n == 2) {
			next.alive[coord] = struct{}{}
		}
	}
	return next
}

// ---- Helpers pour convertir les messages protobuf <-> Board ----

func initBoard(req *pb.BoardRequest) *Board {
	b := &Board{
		width:  req.Width,
		height: req.Height,
		alive:  make(map[[2]int32]struct{}),
	}
	for _, c := range req.Alive {
		b.alive[[2]int32{c.X, c.Y}] = struct{}{}
	}
	return b
}

func toCells(alive map[[2]int32]struct{}) []*pb.Cell {
	cells := make([]*pb.Cell, 0, len(alive))
	for coord := range alive {
		cells = append(cells, &pb.Cell{X: coord[0], Y: coord[1]})
	}
	return cells
}

// ---- Implémentation du service gRPC ----

type lifeServer struct {
	pb.UnimplementedGameOfLifeServer
}

// StreamBoards ouvre un flux serveur infini : chaque TickMs on envoie la grille.
func (s *lifeServer) StreamBoards(req *pb.BoardRequest, stream pb.GameOfLife_StreamBoardsServer) error {
	board := initBoard(req)

	// Conversion de la durée (ms flottantes) en time.Duration
	d := time.Duration(req.TickMs * float64(time.Millisecond))
	if d <= 0 {
		d = 200 * time.Millisecond // valeur par défaut
	}
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	var gen int32 = 0

	for {
		select {
		case <-stream.Context().Done():
			return nil // client déconnecté
		case <-ticker.C:
			resp := &pb.Board{
				Generation: gen,
				Alive:      toCells(board.alive),
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
			board = board.Next()
			gen++
		}
	}
}

// ---- Point d’entrée ----

func main() {
	// Écoute sur :50051
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("échec Listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterGameOfLifeServer(grpcServer, &lifeServer{})

	log.Println("Game of Life gRPC server prêt sur :50051 …")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("échec Serve: %v", err)
	}
}
