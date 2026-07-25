package rabbitclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Response struct {
	Data json.RawMessage
	Err  error
}

type PendingCall struct {
	respCh chan Response
	cancel func()
}

func (p *PendingCall) Wait(ctx context.Context) (json.RawMessage, error) {
	select {
	case resp := <-p.respCh:
		return resp.Data, resp.Err
	case <-ctx.Done():
		p.cancel()
		return nil, ctx.Err()
	}
}

func (p *PendingCall) Cancel() { p.cancel() }

type Client struct {
	conn       *amqp.Connection
	ch         *amqp.Channel
	queueName  string
	replyQueue string

	// amqp091-go channels are not safe for concurrent Publish calls from
	// multiple goroutines. Submit() is meant to be called concurrently —
	// that's the whole point of correlation IDs — so the Publish itself
	// needs to be serialized even though the reply-side demuxing doesn't.
	publishMu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan Response
}

func Dial(amqpURL, queueName string) (*Client, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("declare task queue: %w", err)
	}

	replyQ, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("declare reply queue: %w", err)
	}

	deliveries, err := ch.Consume(replyQ.Name, "", true, true, false, false, nil)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("consume reply queue: %w", err)
	}

	c := &Client{
		conn:       conn,
		ch:         ch,
		queueName:  queueName,
		replyQueue: replyQ.Name,
		pending:    make(map[string]chan Response),
	}
	go c.consumeReplies(deliveries)
	return c, nil
}

func (c *Client) consumeReplies(deliveries <-chan amqp.Delivery) {
	for d := range deliveries {
		c.mu.Lock()
		ch, ok := c.pending[d.CorrelationId]
		if ok {
			delete(c.pending, d.CorrelationId)
		}
		c.mu.Unlock()

		if !ok {
			continue
		}
		ch <- Response{Data: d.Body}
	}
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *Client) Submit(ctx context.Context, event string, data any) (*PendingCall, error) {
	payload, err := json.Marshal(struct {
		Event string `json:"event"`
		Data  any    `json:"data"`
	}{Event: event, Data: data})
	if err != nil {
		return nil, fmt.Errorf("marshaling task: %w", err)
	}

	corrID := randomID()
	respCh := make(chan Response, 1)

	c.mu.Lock()
	c.pending[corrID] = respCh
	c.mu.Unlock()

	cancel := func() {
		c.mu.Lock()
		delete(c.pending, corrID)
		c.mu.Unlock()
	}

	c.publishMu.Lock()
	err = c.ch.PublishWithContext(ctx, "", c.queueName, false, false, amqp.Publishing{
		ContentType:   "application/json",
		CorrelationId: corrID,
		ReplyTo:       c.replyQueue,
		Body:          payload,
	})
	c.publishMu.Unlock()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("publishing task: %w", err)
	}

	return &PendingCall{respCh: respCh, cancel: cancel}, nil
}

func (c *Client) Call(ctx context.Context, event string, data any) (json.RawMessage, error) {
	pc, err := c.Submit(ctx, event, data)
	if err != nil {
		return nil, err
	}
	return pc.Wait(ctx)
}

func (c *Client) Close() error {
	_ = c.ch.Close()
	return c.conn.Close()
}
