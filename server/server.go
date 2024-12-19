package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	pb "example.com/service"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type Server struct {
	pb.UnimplementedServerServer
	redisClient *redis.Client
}

func NewServer() *Server {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "",
		DB:       0,
	})

	_, err := redisClient.Ping(context.Background()).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	return &Server{
		redisClient: redisClient,
	}
}

func (s *Server) Communicate(stream pb.Server_CommunicateServer) error {
	var clientID string

	// Expect a ClientHello message from the client
	clientMsg, err := stream.Recv()
	if err != nil {
		log.Printf("Error receiving ClientHello: %v", err)
		return err
	}

	if hello := clientMsg.GetClientHello(); hello != nil {
		clientID = hello.ClientId
		log.Printf("New client connected with clientID: %s", clientID)
	} else {
		return fmt.Errorf("expected ClientHello as first message")
	}

	defer func() {
		log.Printf("Client disconnected: %s", clientID)
		s.updateClientStatus(clientID, false)
	}()

	go s.startPingingClient(clientID, stream)

	// Handle incoming PONG messages
	for {
		clientMsg, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving message from client: %v", err)
			return err
		}

		if pong := clientMsg.GetPong(); pong != nil {
			log.Printf("Received Pong with status: %s from clientID: %s", pong.Status.String(), clientID)
			s.updateClientStatus(clientID, true)
		} else {
			log.Println("Received unknown message type from client.")
		}
	}
}

func (s *Server) startPingingClient(clientID string, stream pb.Server_CommunicateServer) {
	for {
		time.Sleep(1 * time.Second)
		err := stream.Send(&pb.Ping{Message: "Ping from Server"})
		if err != nil {
			log.Printf("Failed to send Ping to client %s: %v", clientID, err)
			return
		}
		log.Printf("Sent Ping to client: %s", clientID)
	}
}

func (s *Server) updateClientStatus(clientID string, isOnline bool) {
	redisKey := "user:" + clientID
	ctx := context.Background()

	exists, err := s.redisClient.Exists(ctx, redisKey).Result()
	if err != nil {
		log.Printf("Failed to check Redis for client %s: %v", clientID, err)
		return
	}

	statusChanged := false

	if isOnline {
		if exists > 0 {
			err = s.redisClient.Expire(ctx, redisKey, 5*time.Second).Err()
			if err != nil {
				log.Printf("Failed to refresh TTL for client %s: %v", clientID, err)
			}
		} else {
			err = s.redisClient.Set(ctx, redisKey, "online", 5*time.Second).Err()
			if err != nil {
				log.Printf("Failed to set online status for client %s: %v", clientID, err)
			}
			statusChanged = true
		}
	} else {
		if exists > 0 {
			err = s.redisClient.Del(ctx, redisKey).Err()
			if err != nil {
				log.Printf("Failed to remove status for client %s: %v", clientID, err)
			}
			statusChanged = true
		}
	}

	if statusChanged {
		channel := fmt.Sprintf("status_updates:%s", clientID)
		payload := "false"
		if isOnline {
			payload = "true"
		}
		err = s.redisClient.Publish(ctx, channel, payload).Err()
		if err != nil {
			log.Printf("Failed to publish status update for client %s: %v", clientID, err)
		}
		log.Printf("Published status update for client %s: %v", clientID, payload)
	}
}

func (s *Server) FriendListener(stream pb.Server_FriendListenerServer) error {
	ctx := stream.Context()

	// Start a goroutine to send periodic KeepAlivePing to the client
	ticker := time.NewTicker(30 * time.Second) // Send every 30 seconds
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Println("Context done, stopping periodic KeepAlivePing for client")
				return
			case <-ticker.C:
				// Send the KeepAlivePing
				err := stream.Send(&pb.FriendListenerResponse{
					Message: &pb.FriendListenerResponse_KeepalivePing{
						KeepalivePing: &pb.KeepAlivePing{
							Message: "KeepAlivePing from server",
						},
					},
				})
				if err != nil {
					log.Printf("❌ Failed to send KeepAlivePing: %v", err)
					return
				}
				log.Println("✅ Sent KeepAlivePing to client")
			}
		}
	}()

	for {
		clientMsg, err := stream.Recv()
		if err != nil {
			log.Printf("❌ Error receiving from client: %v", err)
			return err
		}

		// Handle the oneof message from the client (either FriendList or KeepAliveAck)
		switch msg := clientMsg.Message.(type) {

		// Handle FriendList sent by the client
		case *pb.FriendListenerRequest_FriendList:
			friendList := msg.FriendList
			log.Printf("📥 Received friend list: %v", friendList.FriendIds)

			// Send the current online status of each friend to the client
			for _, friendID := range friendList.FriendIds {
				redisKey := "user:" + friendID
				status, _ := s.redisClient.Get(ctx, redisKey).Result()
				isOnline := (status == "online")

				err := stream.Send(&pb.FriendListenerResponse{
					Message: &pb.FriendListenerResponse_FriendUpdate{
						FriendUpdate: &pb.FriendUpdate{
							ClientId: friendID,
							IsOnline: isOnline,
						},
					},
				})
				if err != nil {
					log.Printf("❌ Failed to send friend status update for %s: %v", friendID, err)
				}

				// Subscribe to changes for each friend in Redis
				channel := fmt.Sprintf("status_updates:%s", friendID)
				pubsub := s.redisClient.Subscribe(ctx, channel)

				go func(friendID string) {
					for msg := range pubsub.Channel() {
						isOnline := msg.Payload == "true"
						err := stream.Send(&pb.FriendListenerResponse{
							Message: &pb.FriendListenerResponse_FriendUpdate{
								FriendUpdate: &pb.FriendUpdate{
									ClientId: friendID,
									IsOnline: isOnline,
								},
							},
						})
						if err != nil {
							log.Printf("❌ Failed to send friend status update for %s: %v", friendID, err)
							return
						}
						log.Printf("✅ Sent friend status update for %s: %v", friendID, isOnline)
					}
				}(friendID)
			}

		// Handle KeepAliveAck sent by the client
		case *pb.FriendListenerRequest_KeepaliveAck:
			ack := msg.KeepaliveAck
			log.Printf("📥 Received KeepAliveAck from client: %v", ack.Message)

		default:
			log.Printf("❌ Unknown message type received from client: %v", clientMsg)
		}
	}
}

func (s *Server) GetAllUserInfo(ctx context.Context, req *pb.Empty) (*pb.UserList, error) {
	log.Println("Fetching all users from ElastiCache...")
	var users []*pb.UserInfo

	ctx = context.Background()
	cursor := uint64(0)
	scanCount := int64(100) // Number of keys to scan at a time
	redisPrefix := "user:"

	for {
		// Scan keys with "user:" prefix
		keys, nextCursor, err := s.redisClient.Scan(ctx, cursor, redisPrefix+"*", scanCount).Result()
		if err != nil {
			log.Printf("Failed to scan Redis keys: %v", err)
			return nil, err
		}

		for _, key := range keys {
			clientID := key[len(redisPrefix):] // Remove "user:" prefix
			status, err := s.redisClient.Get(ctx, key).Result()
			if err != nil {
				log.Printf("Failed to get status for key %s: %v", key, err)
				status = "unknown"
			}

			users = append(users, &pb.UserInfo{
				ClientId: clientID,
				Status:   status,
			})
		}

		// If nextCursor is 0, we have scanned all keys
		if nextCursor == 0 {
			break
		}

		cursor = nextCursor
	}

	log.Printf("Found %d users in ElastiCache", len(users))
	return &pb.UserList{Users: users}, nil
}

func main() {
	//
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	go http.ListenAndServe(":8080", nil)
	//

	listener, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen on port 50051: %v", err)
	}

	grpcServer := grpc.NewServer()

	server := NewServer()

	pb.RegisterServerServer(grpcServer, server)
	reflection.Register(grpcServer)

	log.Printf("Server is running on port 50051: 30secs ping added")
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
