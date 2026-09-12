// Command nock is the CLI affordance for every real NOCK operation (founder real-time,
// 2026-09-12: "add cli affordances for everything in our app FIRST as much as possible" -- so
// agents, not just a future GUI, can drive texture creation). It's a thin dispatcher over
// internal/nock.Service, the exact same package internal/http/handlers/nock.go's own HTTP API
// calls -- "same shape CLI and GUI," the founder's own framing, achieved here by literally
// sharing one Go package as the only place any real logic lives.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"iduna/internal/nock"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	dataDir := os.Getenv("NOCK_DATA_DIR")
	if dataDir == "" {
		dataDir = "./nock-projects"
	}
	svc, err := nock.NewService(dataDir)
	if err != nil {
		fatal(err)
	}

	cmd := os.Args[1]
	args := os.Args[2:]
	var runErr error
	switch cmd {
	case "init":
		runErr = cmdInit(svc, args)
	case "list":
		runErr = cmdList(svc, args)
	case "show":
		runErr = cmdShow(svc, args)
	case "delete":
		runErr = cmdDelete(svc, args)
	case "layer-add":
		runErr = cmdLayerAdd(svc, args)
	case "layer-remove":
		runErr = cmdLayerRemove(svc, args)
	case "layer-opacity":
		runErr = cmdLayerOpacity(svc, args)
	case "layer-visible":
		runErr = cmdLayerVisible(svc, args)
	case "layer-move":
		runErr = cmdLayerMove(svc, args)
	case "layer-mask":
		runErr = cmdLayerMask(svc, args)
	case "layer-unmask":
		runErr = cmdLayerUnmask(svc, args)
	case "gradient":
		runErr = cmdGradient(svc, args)
	case "hue-sat":
		runErr = cmdHueSat(svc, args)
	case "sharpen":
		runErr = cmdSharpen(svc, args)
	case "resize":
		runErr = cmdResize(svc, args)
	case "export":
		runErr = cmdExport(svc, args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "nock: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if runErr != nil {
		fatal(runErr)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "nock:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `nock — layered image editor CLI (ImageMagick-backed)

Usage: nock <command> [flags]

Project:
  init -project NAME -width W -height H
  list
  show -project NAME
  delete -project NAME

Layers:
  layer-add     -project NAME -layer NAME -file PATH
  layer-remove  -project NAME -layer NAME
  layer-opacity -project NAME -layer NAME -opacity 0-100
  layer-visible -project NAME -layer NAME -visible true|false
  layer-move    -project NAME -layer NAME -delta N   (positive = move up/later)
  layer-mask    -project NAME -layer NAME -file PATH
  layer-unmask  -project NAME -layer NAME

Effects:
  gradient -project NAME -layer NAME -from '#RRGGBB' -to '#RRGGBB' [-direction vertical|horizontal]
  hue-sat  -project NAME -layer NAME [-brightness 100] [-saturation 100] [-hue 100]
  sharpen  -project NAME -layer NAME [-radius 0] [-sigma 1] [-amount 1]
  resize   -project NAME -width W -height H

Export:
  export -project NAME -out PATH.png|PATH.jpg [-background '#RRGGBB']

Env:
  NOCK_DATA_DIR  root directory holding every project (default ./nock-projects)
`)
}

func printProject(p *nock.Project) {
	data, _ := json.MarshalIndent(p, "", "  ")
	fmt.Println(string(data))
}

func cmdInit(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	width := fs.Int("width", 512, "canvas width")
	height := fs.Int("height", 512, "canvas height")
	fs.Parse(args)
	if *project == "" {
		return fmt.Errorf("-project is required")
	}
	p, err := svc.CreateProject(*project, *width, *height)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdList(svc *nock.Service, args []string) error {
	names, err := svc.ListProjects()
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Println(n)
	}
	return nil
}

func cmdShow(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	fs.Parse(args)
	p, err := svc.GetProject(*project)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdDelete(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	fs.Parse(args)
	return svc.DeleteProject(*project)
}

func cmdLayerAdd(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("layer-add", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	file := fs.String("file", "", "source image path")
	fs.Parse(args)
	p, err := svc.AddLayer(*project, *layer, *file)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdLayerRemove(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("layer-remove", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	fs.Parse(args)
	p, err := svc.RemoveLayer(*project, *layer)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdLayerOpacity(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("layer-opacity", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	opacity := fs.Int("opacity", 100, "0-100")
	fs.Parse(args)
	p, err := svc.SetOpacity(*project, *layer, *opacity)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdLayerVisible(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("layer-visible", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	visible := fs.Bool("visible", true, "true or false")
	fs.Parse(args)
	p, err := svc.SetVisible(*project, *layer, *visible)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdLayerMove(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("layer-move", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	delta := fs.Int("delta", 1, "positions to move (positive = up/later in the stack)")
	fs.Parse(args)
	p, err := svc.MoveLayer(*project, *layer, *delta)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdLayerMask(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("layer-mask", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	file := fs.String("file", "", "grayscale mask image path")
	fs.Parse(args)
	p, err := svc.SetMask(*project, *layer, *file)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdLayerUnmask(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("layer-unmask", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	fs.Parse(args)
	p, err := svc.ClearMask(*project, *layer)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdGradient(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("gradient", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "new layer name")
	from := fs.String("from", "#000000", "start color")
	to := fs.String("to", "#ffffff", "end color")
	direction := fs.String("direction", "vertical", "vertical or horizontal")
	fs.Parse(args)
	p, err := svc.AddGradientLayer(*project, *layer, *from, *to, *direction)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdHueSat(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("hue-sat", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	brightness := fs.Int("brightness", 100, "percent, 100 = unchanged")
	saturation := fs.Int("saturation", 100, "percent, 100 = unchanged")
	hue := fs.Int("hue", 100, "percent, 100 = unchanged")
	fs.Parse(args)
	p, err := svc.AdjustHueSaturation(*project, *layer, *brightness, *saturation, *hue)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdSharpen(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("sharpen", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	layer := fs.String("layer", "", "layer name")
	radius := fs.Float64("radius", 0, "unsharp radius")
	sigma := fs.Float64("sigma", 1, "unsharp sigma")
	amount := fs.Float64("amount", 1, "unsharp amount")
	fs.Parse(args)
	p, err := svc.Sharpen(*project, *layer, *radius, *sigma, *amount)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdResize(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("resize", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	width := fs.Int("width", 0, "new canvas width")
	height := fs.Int("height", 0, "new canvas height")
	fs.Parse(args)
	p, err := svc.ResizeCanvas(*project, *width, *height)
	if err != nil {
		return err
	}
	printProject(p)
	return nil
}

func cmdExport(svc *nock.Service, args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	out := fs.String("out", "", "output file path (.png or .jpg)")
	background := fs.String("background", "", "background color for JPEG export (default white)")
	fs.Parse(args)
	if err := svc.Export(*project, *out, *background); err != nil {
		return err
	}
	fmt.Println(*out)
	return nil
}
