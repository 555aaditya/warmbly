package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	gpubsub "cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/apiv1/pubsubpb"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

type localPublisher struct {
	pubsubpb.UnimplementedPublisherServer
	mu         sync.Mutex
	topics     map[string]bool
	gets       int
	creates    int
	messages   []*pubsubpb.PubsubMessage
	lookupErr  error
	publishErr error
}

func (s *localPublisher) GetTopic(_ context.Context, req *pubsubpb.GetTopicRequest) (*pubsubpb.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	if !s.topics[req.Topic] {
		return nil, status.Error(codes.NotFound, "topic missing")
	}
	return &pubsubpb.Topic{Name: req.Topic}, nil
}

func (s *localPublisher) CreateTopic(_ context.Context, topic *pubsubpb.Topic) (*pubsubpb.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creates++
	if s.topics[topic.Name] {
		return nil, status.Error(codes.AlreadyExists, "topic exists")
	}
	s.topics[topic.Name] = true
	return topic, nil
}

func (s *localPublisher) DeleteTopic(_ context.Context, req *pubsubpb.DeleteTopicRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.topics, req.Topic)
	return &emptypb.Empty{}, nil
}

func (s *localPublisher) Publish(_ context.Context, req *pubsubpb.PublishRequest) (*pubsubpb.PublishResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publishErr != nil {
		return nil, s.publishErr
	}
	if !s.topics[req.Topic] {
		return nil, status.Error(codes.NotFound, "topic missing")
	}
	s.messages = append(s.messages, req.Messages...)
	ids := make([]string, len(req.Messages))
	for i := range ids {
		ids[i] = "local-message"
	}
	return &pubsubpb.PublishResponse{MessageIds: ids}, nil
}

func localClient(t testing.TB) (*Client, *localPublisher) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	fake := &localPublisher{topics: map[string]bool{}}
	pubsubpb.RegisterPublisherServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///local-pubsub", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}))
	if err != nil {
		t.Fatal(err)
	}
	client, err := gpubsub.NewClient(context.Background(), "local-project", option.WithGRPCConn(conn))
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{client: client, projectID: "local-project"}
	t.Cleanup(func() {
		_ = c.Close()
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	})
	return c, fake
}

func TestClientReusesPublisherAndRecreatesDeletedTopic(t *testing.T) {
	c, fake := localClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data := map[string]string{"event": "synthetic"}
	attrs := map[string]string{"organization_id": "fixture-org"}
	first, err := c.getTopic(ctx, TopicUserEvents)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := c.Publish(ctx, TopicUserEvents, data, attrs); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.client.Topic(TopicUserEvents).Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, TopicUserEvents, data, attrs); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.gets != 22 || fake.creates != 2 || len(fake.messages) != 21 {
		t.Fatalf("gets=%d creates=%d published=%d", fake.gets, fake.creates, len(fake.messages))
	}
	want, _ := json.Marshal(data)
	for _, message := range fake.messages {
		if string(message.Data) != string(want) || !reflect.DeepEqual(message.Attributes, attrs) {
			t.Fatal("published payload or routing attributes changed")
		}
	}
	if len(c.topics) != 1 || c.topics[TopicUserEvents] != first {
		t.Fatal("publishes did not share one topic publisher")
	}
}

func TestClientPublisherConcurrentReuseAndClose(t *testing.T) {
	c, _ := localClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := c.getTopic(ctx, TopicUserEvents)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			got, err := c.getTopic(ctx, TopicUserEvents)
			if err != nil || got != first {
				t.Errorf("concurrent lookup: topic=%p err=%v", got, err)
			}
			if err := c.Publish(ctx, TopicUserEvents, "fixture", nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Publish(ctx, &gpubsub.Message{Data: []byte("fixture")}).Get(ctx); !errors.Is(err, gpubsub.ErrTopicStopped) {
		t.Fatalf("publisher not stopped: %v", err)
	}
	if err := c.Publish(ctx, TopicUserEvents, "fixture", nil); !errors.Is(err, gpubsub.ErrTopicStopped) {
		t.Fatalf("publish after close: %v", err)
	}
}

func TestClientDoesNotCacheLookupOrPublishErrors(t *testing.T) {
	c, fake := localClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fake.lookupErr = status.Error(codes.PermissionDenied, "fixture lookup denied")
	if err := c.Publish(ctx, TopicUserEvents, "fixture", nil); status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("lookup error lost: %v", err)
	}
	fake.mu.Lock()
	fake.lookupErr = nil
	fake.publishErr = status.Error(codes.PermissionDenied, "fixture publish denied")
	fake.mu.Unlock()
	if err := c.Publish(ctx, TopicUserEvents, "fixture", nil); status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("publish error lost: %v", err)
	}
	fake.mu.Lock()
	fake.publishErr = nil
	fake.mu.Unlock()
	if err := c.Publish(ctx, TopicUserEvents, "fixture", nil); err != nil {
		t.Fatalf("retry after error: %v", err)
	}
	if err := c.Publish(ctx, TopicUserEvents, make(chan int), nil); err == nil {
		t.Fatal("marshal error was suppressed")
	}
}

func BenchmarkClientPublisherReuse(b *testing.B) {
	for _, mode := range []string{"per_message", "per_topic"} {
		b.Run(mode, func(b *testing.B) {
			c, _ := localClient(b)
			ctx := context.Background()
			var publishers []*gpubsub.Topic
			b.Cleanup(func() {
				for _, topic := range publishers {
					topic.Stop()
				}
			})
			b.ReportAllocs()
			for b.Loop() {
				if mode == "per_topic" {
					if err := c.Publish(ctx, TopicUserEvents, "fixture", nil); err != nil {
						b.Fatal(err)
					}
					continue
				}
				// The BASE path allocates a publisher for every message and never stops it.
				topic := c.client.Topic(TopicUserEvents)
				exists, err := topic.Exists(ctx)
				if err != nil {
					b.Fatal(err)
				}
				if !exists {
					topic, err = c.client.CreateTopic(ctx, TopicUserEvents)
					if err != nil {
						b.Fatal(err)
					}
				}
				data, err := json.Marshal("fixture")
				if err != nil {
					b.Fatal(err)
				}
				if _, err := topic.Publish(ctx, &gpubsub.Message{Data: data}).Get(ctx); err != nil {
					b.Fatal(err)
				}
				publishers = append(publishers, topic)
			}
			if mode == "per_message" {
				b.ReportMetric(float64(len(publishers))/float64(b.N), "publishers/op")
			} else {
				b.ReportMetric(float64(len(c.topics))/float64(b.N), "publishers/op")
			}
		})
	}
}
