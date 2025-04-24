// client/main.go — affiche une fenêtre 100×100 et mesure la latence serveur→client
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text"
	pb "github.com/ugora/gameoflife/proto"
	"golang.org/x/image/font/basicfont"
	"google.golang.org/grpc"
)

var (
	viewX = flag.Int("x", 0, "coin X (0-499)")
	viewY = flag.Int("y", 0, "coin Y (0-499)")
)

const (
	w, h = 100, 100 // fenêtre logique
	cell = 5        // px / cellule
	winW = w * cell
	winH = h * cell
)

type game struct {
	mu         sync.RWMutex
	live       map[[2]int32]struct{}
	latencyMs  float64
	generation int32
}

func (g *game) Update() error { return nil }

func (g *game) Draw(scr *ebiten.Image) {
	scr.Fill(color.Black)

	g.mu.RLock()
	for c := range g.live {
		x, y := int(c[0])*cell, int(c[1])*cell
		scr.SubImage(image.Rect(x, y, x+cell, y+cell)).(*ebiten.Image).Fill(color.White)
	}
	gen := g.generation
	lat := g.latencyMs
	g.mu.RUnlock()

	msg := fmt.Sprintf("Gen:%d  Lat:%.1f ms  FPS:%.1f", gen, lat, ebiten.CurrentFPS())
	text.Draw(scr, msg, basicfont.Face7x13, 8, 16, color.RGBA{255, 255, 255, 255})
}

func (g *game) Layout(_, _ int) (int, int) { return winW, winH }

func main() {
	flag.Parse()

	// connexion gRPC
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	cl := pb.NewGameOfLifeClient(conn)

	// sous-cription à la vue demandée
	stream, err := cl.StreamBoards(context.Background(), &pb.ViewportRequest{
		ViewX: int32(*viewX), ViewY: int32(*viewY),
		ViewW: w, ViewH: h,
	})
	if err != nil {
		log.Fatalf("StreamBoards: %v", err)
	}

	g := &game{live: map[[2]int32]struct{}{}}

	// goroutine de réception
	go func() {
		for {
			b, err := stream.Recv()
			if err != nil {
				return
			}
			now := time.Now()
			sent := time.Unix(0, b.SentUnixns) // champ ajouté côté serveur
			tmp := make(map[[2]int32]struct{}, len(b.Alive))
			for _, c := range b.Alive {
				tmp[[2]int32{c.X, c.Y}] = struct{}{}
			}

			g.mu.Lock()
			g.live = tmp
			g.latencyMs = now.Sub(sent).Seconds() * 1000
			g.generation = b.Generation
			g.mu.Unlock()
		}
	}()

	ebiten.SetWindowSize(winW, winH)
	title := fmt.Sprintf("Viewer (%d,%d)", *viewX, *viewY)
	ebiten.SetWindowTitle(title)
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
