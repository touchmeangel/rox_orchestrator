package dockerx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
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

type PullProgressFunc func(status, id string, current, total int64)

func (c *Client) EnsureImage(ctx context.Context, ref string, onProgress PullProgressFunc) error {
	if _, _, err := c.cli.ImageInspectWithRaw(ctx, ref); err == nil {
		return nil
	}
	return c.PullImage(ctx, ref, onProgress)
}

func (c *Client) PullImage(ctx context.Context, ref string, onProgress PullProgressFunc) error {
	auth, err := registryAuth(ref)
	if err != nil {
		auth = ""
	}
	rc, err := c.cli.ImagePull(ctx, ref, image.PullOptions{RegistryAuth: auth})
	if err != nil {
		return fmt.Errorf("pulling %s: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()

	dec := json.NewDecoder(rc)
	for {
		var msg struct {
			Status         string `json:"status"`
			ID             string `json:"id"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("reading pull progress for %s: %w", ref, err)
		}
		if msg.Error != "" {
			return fmt.Errorf("pulling %s: %s", ref, msg.Error)
		}
		if onProgress != nil {
			onProgress(msg.Status, msg.ID, msg.ProgressDetail.Current, msg.ProgressDetail.Total)
		}
	}
	return nil
}

type LineWriter interface {
	WriteLine(line string)
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
	Runtime    string
	LogFile    io.Writer
	LogPrefix  string
	Quiet      bool
	Live       LineWriter
}

var stdoutMu sync.Mutex

func (c *Client) Run(ctx context.Context, spec RunSpec) (int64, error) {
	if err := c.EnsureImage(ctx, spec.Image, nil); err != nil {
		return -1, fmt.Errorf("ensuring image %s is available: %w", spec.Image, err)
	}
	_ = c.cli.ContainerRemove(ctx, spec.Name, container.RemoveOptions{Force: true})
	mounts := make([]mount.Mount, 0, len(spec.Mounts))
	for _, m := range spec.Mounts {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	cfg := &container.Config{
		Image: spec.Image,
		Cmd:   spec.Cmd,
		Env:   spec.Env,
		Tty:   true,
	}
	hostCfg := &container.HostConfig{
		Mounts:     mounts,
		ExtraHosts: spec.ExtraHosts,
		AutoRemove: true,
		Runtime:    spec.Runtime,
	}
	resp, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, spec.Name)
	if err != nil {
		return -1, fmt.Errorf("creating container: %w", err)
	}
	id := resp.ID
	if err := c.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return -1, fmt.Errorf("starting container: %w", err)
	}
	logsCtx, cancelLogs := context.WithCancel(ctx)
	defer cancelLogs()
	go c.streamLogs(logsCtx, id, spec.LogFile, spec.LogPrefix, spec.Quiet, spec.Live)
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

func (c *Client) streamLogs(ctx context.Context, containerID string, logFile io.Writer, prefix string, quiet bool, live LineWriter) {
	out, err := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		return
	}
	defer func() { _ = out.Close() }()
	pw := &Writer{prefix: prefix, quiet: quiet, live: live}
	var mw io.Writer = pw
	if logFile != nil {
		mw = io.MultiWriter(pw, logFile)
	}
	_, _ = io.Copy(mw, out)
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

type Writer struct {
	buf    []byte
	prefix string
	quiet  bool
	live   LineWriter
}

func (p *Writer) Write(data []byte) (int, error) {
	p.buf = append(p.buf, data...)
	for {
		idx := bytes.IndexByte(p.buf, '\n')
		if idx < 0 {
			break
		}
		line := p.buf[:idx]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if !p.quiet {
			text := p.prefix + string(line)
			if p.live != nil {
				p.live.WriteLine(text)
			} else {
				stdoutMu.Lock()
				fmt.Println(text)
				stdoutMu.Unlock()
			}
		}
		p.buf = p.buf[idx+1:]
	}
	return len(data), nil
}
