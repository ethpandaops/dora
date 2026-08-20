package replay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const consoleHelp = `commands:
  status              show where the replay currently stands
  step [n]            advance n slots (default 1), then pause
  forward <slot> [speed]
                      run to a slot, emitting every slot on the way; without a
                      speed it advances as fast as upstream allows
  play [speed]        run continuously at <speed>x real time (default 1)
  stop                pause the replay and freeze the virtual clock
  start               resume at the last speed
  help                show this help
  quit                stop the replay and exit
`

// RunConsole reads commands from stdin until the input ends or `quit` is entered. It
// returns when the replay should shut down.
func (r *Replay) RunConsole(ctx context.Context, in io.Reader, out io.Writer) {
	lines := make(chan string)

	go func() {
		defer close(lines)

		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	consolePrint(out, consoleHelp)
	r.printStatus(out)
	consolePrint(out, "replay> ")

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}

			if r.runCommand(out, line) {
				return
			}

			consolePrint(out, "replay> ")
		}
	}
}

// runCommand executes one console line and reports whether the replay should exit.
func (r *Replay) runCommand(out io.Writer, line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}

	command, args := fields[0], fields[1:]

	switch command {
	case "status", "s":
		r.printStatus(out)

	case "step":
		slots := uint64(1)

		if len(args) > 0 {
			parsed, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				consolePrintf(out, "invalid slot count %q\n", args[0])

				return false
			}

			slots = parsed
		}

		r.Step(slots)

	case "forward", "fwd":
		if len(args) == 0 {
			consolePrint(out, "usage: forward <slot> [speed]\n")

			return false
		}

		slot, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			consolePrintf(out, "invalid slot %q\n", args[0])

			return false
		}

		speed := 0.0

		if len(args) > 1 {
			speed, err = parseSpeed(args[1])
			if err != nil {
				consolePrintf(out, "%v\n", err)

				return false
			}
		}

		if err := r.Forward(slot, speed); err != nil {
			consolePrintf(out, "%v\n", err)
		}

	case "play":
		speed := 1.0

		if len(args) > 0 {
			parsed, err := parseSpeed(args[0])
			if err != nil {
				consolePrintf(out, "%v\n", err)

				return false
			}

			speed = parsed
		}

		r.Play(speed)

	case "stop", "pause":
		r.Pause()
		r.printStatus(out)

	case "start", "resume":
		r.Resume()

	case "help", "?":
		consolePrint(out, consoleHelp)

	case "quit", "exit", "q":
		return true

	default:
		consolePrintf(out, "unknown command %q (try `help`)\n", command)
	}

	return false
}

func (r *Replay) printStatus(out io.Writer) {
	status := r.Status()

	mode := "paused"

	switch {
	case status.Holding:
		mode = fmt.Sprintf("holding for %v state load(s)", status.StateLoads)
	case status.Running && status.Speed > 0:
		mode = fmt.Sprintf("playing %gx", status.Speed)
	case status.Running && status.TargetSlot > 0:
		mode = fmt.Sprintf("stepping to %v", status.TargetSlot)
	case status.Running:
		mode = "running at max speed"
	}

	upstream := status.Upstream
	if status.Tracoor {
		upstream += " (+tracoor)"
	}

	consolePrintf(out, "  slot %v  epoch %v  head %v [%v]\n",
		status.VirtualSlot, status.VirtualEpoch, status.HeadSlot, shortRoot(status.HeadRoot))
	consolePrintf(out, "  justified %v  finalized %v  el block %v\n",
		status.JustifiedEpoch, status.FinalizedEpoch, status.ExecutionBlock)
	consolePrintf(out, "  %v  time %v  streams %v\n",
		mode, status.VirtualTime.Format(time.RFC3339), status.Subscribers)
	consolePrintf(out, "  upstream %v\n", upstream)
}

// parseSpeed reads a playback rate, accepting both `4` and `4x`.
func parseSpeed(value string) (float64, error) {
	speed, err := strconv.ParseFloat(strings.TrimSuffix(value, "x"), 64)
	if err != nil || speed <= 0 {
		return 0, fmt.Errorf("invalid speed %q", value)
	}

	return speed, nil
}

// consolePrint and consolePrintf write to the console; a broken console is not worth
// aborting a replay for, so write errors are deliberately ignored.
func consolePrint(out io.Writer, text string) {
	_, _ = io.WriteString(out, text)
}

func consolePrintf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

func shortRoot(root string) string {
	if len(root) <= 12 {
		return root
	}

	return root[:10] + "…"
}

// Stdio returns the console's default streams, kept in one place so the caller does not
// have to reach into os from its own package.
func Stdio() (io.Reader, io.Writer) {
	return os.Stdin, os.Stdout
}
