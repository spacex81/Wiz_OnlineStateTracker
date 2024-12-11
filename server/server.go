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

	for {
		clientMsg, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving from client: %v", err)
			return err
		}

		if friendList := clientMsg.GetFriendList(); friendList != nil {
			log.Printf("Received friend list: %v", friendList.FriendIds)

			for _, friendID := range friendList.FriendIds {
				redisKey := "user:" + friendID
				status, _ := s.redisClient.Get(ctx, redisKey).Result()
				isOnline := (status == "online")
				stream.Send(&pb.FriendStatusUpdate{
					ClientId: friendID,
					IsOnline: isOnline,
				})

				channel := fmt.Sprintf("status_updates:%s", friendID)
				pubsub := s.redisClient.Subscribe(ctx, channel)

				go func(friendID string) {
					for msg := range pubsub.Channel() {
						isOnline := msg.Payload == "true"
						err := stream.Send(&pb.FriendStatusUpdate{
							ClientId: friendID,
							IsOnline: isOnline,
						})
						if err != nil {
							log.Printf("Failed to send friend status update for %s: %v", friendID, err)
							return
						}
					}
				}(friendID)
			}
		}
	}
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

	log.Printf("Server is running on port 50051")
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
