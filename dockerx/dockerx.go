package dockerx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

type Client struct {
	cli *client.Client
}

func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("connecting to docker daemon: %w", err)
	}
	return &Client{cli: cli}, nil
}

func (c *Client) Close() error { return c.cli.Close() }

func (c *Client) EnsureImage(ctx context.Context, ref string) error {
	if _, _, err := c.cli.ImageInspectWithRaw(ctx, ref); err == nil {
		return nil
	}
	return c.PullImage(ctx, ref)
}

func (c *Client) PullImage(ctx context.Context, ref string) error {
	rc, err := c.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling %s: %w", ref, err)
	}
	defer rc.Close()
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

type RunSpec struct {
	Image      string
	Name       string
	Cmd        []string
	Env        []string
	Mounts     []Mount
	ExtraHosts []string
	LogPrefix  string
	LogFile    io.Writer
}

var stdoutMu sync.Mutex

func (c *Client) Run(ctx context.Context, spec RunSpec) (int64, error) {
	_ = c.cli.ContainerRemove(ctx, spec.Name, container.RemoveOptions{Force: true})

	mounts := make([]mount.Mount, 0, len(spec.Mounts))
	for _, m := range spec.Mounts {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}

	cfg := &container.Config{Image: spec.Image, Tty: true, Cmd: spec.Cmd, Env: spec.Env}
	if u := currentUserSpec(); u != "" {
		cfg.User = u
	}
	hostCfg := &container.HostConfig{Mounts: mounts, ExtraHosts: spec.ExtraHosts, AutoRemove: true}

	resp, err := c.cli.ContainerCreate(
		ctx,
		cfg,
		hostCfg,
		nil,
		nil,
		spec.Name,
	)
	if err != nil {
		return -1, fmt.Errorf("creating container: %w", err)
	}
	id := resp.ID
	defer c.cli.ContainerRemove(context.Background(), id, container.RemoveOptions{Force: true})

	if err := c.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return -1, fmt.Errorf("starting container: %w", err)
	}

	logsCtx, cancelLogs := context.WithCancel(ctx)
	defer cancelLogs()
	go c.streamLogs(logsCtx, id, spec.LogPrefix, spec.LogFile)

	statusCh, errCh := c.cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return -1, fmt.Errorf("waiting for container: %w", err)
		}
		return 0, nil
	case status := <-statusCh:
		return status.StatusCode, nil
	case <-ctx.Done():
		_ = c.cli.ContainerStop(context.Background(), id, container.StopOptions{})
		return -1, ctx.Err()
	}
}

func (c *Client) streamLogs(ctx context.Context, containerID, prefix string, logFile io.Writer) {
	out, err := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		return
	}
	defer out.Close()

	pw := &prefixWriter{prefix: prefix}
	var mw io.Writer = pw
	if logFile != nil {
		mw = io.MultiWriter(pw, logFile)
	}
	_, _ = stdcopy.StdCopy(mw, mw, out)
}

func (c *Client) Ping(ctx context.Context) bool {
	_, err := c.cli.Ping(ctx)
	return err == nil
}

type prefixWriter struct {
	prefix string
	buf    []byte
}

func (p *prefixWriter) Write(data []byte) (int, error) {
	p.buf = append(p.buf, data...)

	for {
		idx := bytes.IndexByte(p.buf, '\n')
		if idx == -1 {
			break
		}

		line := p.buf[:idx+1]
		fmt.Printf("[%s] %s", p.prefix, string(line))
		p.buf = p.buf[idx+1:]
	}

	return len(data), nil
}

func currentUserSpec() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}
