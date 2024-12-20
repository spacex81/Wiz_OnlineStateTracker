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
		// s.updateClientStatus(clientID, false)
		s.updateClientStatus(clientID, "offline")
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
			// Check if the pong status includes whether the app is in foreground or background
			switch pong.Status {
			case pb.Pong_FOREGROUND:
				log.Printf("Received Pong with status: FOREGROUND from clientID: %s", clientID)
				s.updateClientStatus(clientID, "foreground")
			case pb.Pong_BACKGROUND:
				log.Printf("Received Pong with status: BACKGROUND from clientID: %s", clientID)
				s.updateClientStatus(clientID, "background")
			default:
				log.Printf("Received Pong with unknown status from clientID: %s", clientID)
			}
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

func (s *Server) updateClientStatus(clientID string, newStatus string) {
	redisKey := "user:" + clientID
	ctx := context.Background()
	statusChanged := false // Flag to track if status actually changed

	// Check the current status from Redis
	currentStatus, err := s.redisClient.Get(ctx, redisKey).Result()
	if err != nil && err != redis.Nil {
		log.Printf("Error checking Redis for client %s: %v", clientID, err)
		return
	}

	// Handle the "offline" status case
	if newStatus == "offline" {
		if currentStatus != "" {
			// Client is currently online, remove it from Redis
			err := s.redisClient.Del(ctx, redisKey).Err()
			if err != nil {
				log.Printf("Failed to remove status for client %s: %v", clientID, err)
			} else {
				log.Printf("Client %s set to OFFLINE", clientID)
				statusChanged = true
			}
		} else {
			log.Printf("Client %s is already OFFLINE", clientID)
		}
	} else {
		// Handle "foreground" or "background" statuses
		if currentStatus != newStatus {
			// Status has changed, update Redis with new status
			err := s.redisClient.Set(ctx, redisKey, newStatus, 5*time.Second).Err()
			if err != nil {
				log.Printf("Failed to set %s status for client %s: %v", newStatus, clientID, err)
			} else {
				log.Printf("Updated client %s to %s", clientID, newStatus)
				statusChanged = true
			}
		} else {
			// No status change, but extend the TTL
			err := s.redisClient.Expire(ctx, redisKey, 5*time.Second).Err()
			if err != nil {
				log.Printf("Failed to extend TTL for client %s: %v", clientID, err)
			} else {
				log.Printf("Extended TTL for client %s (status: %s)", clientID, newStatus)
			}
		}
	}

	// If the status changed, publish the change
	if statusChanged {
		channel := fmt.Sprintf("status_updates:%s", clientID)
		err := s.redisClient.Publish(ctx, channel, newStatus).Err()
		if err != nil {
			log.Printf("Failed to publish status change for client %s: %v", clientID, err)
		} else {
			log.Printf("Published status change for client %s: %s", clientID, newStatus)
		}
	}
}

// func (s *Server) FriendListener(stream pb.Server_FriendListenerServer) error {
// 	ctx := stream.Context()

// 	// Start a goroutine to send periodic KeepAlivePing to the client
// 	ticker := time.NewTicker(30 * time.Second) // Send every 30 seconds
// 	defer ticker.Stop()

// 	go func() {
// 		for {
// 			select {
// 			case <-ctx.Done():
// 				log.Println("Context done, stopping periodic KeepAlivePing for client")
// 				return
// 			case <-ticker.C:
// 				// Send the KeepAlivePing
// 				err := stream.Send(&pb.FriendListenerResponse{
// 					Message: &pb.FriendListenerResponse_KeepalivePing{
// 						KeepalivePing: &pb.KeepAlivePing{
// 							Message: "KeepAlivePing from server",
// 						},
// 					},
// 				})
// 				if err != nil {
// 					log.Printf("Failed to send KeepAlivePing: %v", err)
// 					return
// 				}
// 				log.Println("Sent KeepAlivePing to client")
// 			}
// 		}
// 	}()

// 	for {
// 		clientMsg, err := stream.Recv()
// 		if err != nil {
// 			log.Printf("Error receiving from client: %v", err)
// 			return err
// 		}

// 		// Handle the oneof message from the client (either FriendList or KeepAliveAck)
// 		switch msg := clientMsg.Message.(type) {

// 		case *pb.FriendListenerRequest_FriendList:
// 			friendList := msg.FriendList
// 			log.Printf("Received friend list: %v", friendList.FriendIds)

// 			// Send the current status of each friend to the client
// 			for _, friendID := range friendList.FriendIds {
// 				redisKey := "user:" + friendID
// 				status, _ := s.redisClient.Get(ctx, redisKey).Result()
// 				statusEnum := s.mapStatusToEnum(status)

// 				err := stream.Send(&pb.FriendListenerResponse{
// 					Message: &pb.FriendListenerResponse_FriendUpdate{
// 						FriendUpdate: &pb.FriendUpdate{
// 							ClientId: friendID,
// 							Status:   statusEnum,
// 						},
// 					},
// 				})
// 				if err != nil {
// 					log.Printf("Failed to send friend status update for %s: %v", friendID, err)
// 				}

// 				// Subscribe to changes for each friend in Redis
// 				channel := fmt.Sprintf("status_updates:%s", friendID)
// 				pubsub := s.redisClient.Subscribe(ctx, channel)

// 				go func(friendID string) {
// 					for msg := range pubsub.Channel() {
// 						statusEnum := s.mapStatusToEnum(msg.Payload)
// 						err := stream.Send(&pb.FriendListenerResponse{
// 							Message: &pb.FriendListenerResponse_FriendUpdate{
// 								FriendUpdate: &pb.FriendUpdate{
// 									ClientId: friendID,
// 									Status:   statusEnum,
// 								},
// 							},
// 						})
// 						if err != nil {
// 							log.Printf("Failed to send friend status update for %s: %v", friendID, err)
// 							return
// 						}
// 						log.Printf("Sent friend status update for %s: %v", friendID, statusEnum)
// 					}
// 				}(friendID)
// 			}

// 		case *pb.FriendListenerRequest_KeepaliveAck:
// 			ack := msg.KeepaliveAck
// 			log.Printf("Received KeepAliveAck from client: %v", ack.Message)

//			default:
//				log.Printf("Unknown message type received from client: %v", clientMsg)
//			}
//		}
//	}
func (s *Server) FriendListener(stream pb.Server_FriendListenerServer) error {
	ctx := stream.Context()

	// Track the active friends that the server is listening to
	activeFriends := make(map[string]struct{})
	pubsubMap := make(map[string]*redis.PubSub) // Maps friendID to the PubSub subscription

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
					log.Printf("Failed to send KeepAlivePing: %v", err)
					return
				}
				log.Println("Sent KeepAlivePing to client")
			}
		}
	}()

	for {
		clientMsg, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving from client: %v", err)
			return err
		}

		// Handle the oneof message from the client (either AddFriendList, AddFriend, RemoveFriendList, RemoveFriend, or KeepAliveAck)
		switch msg := clientMsg.Message.(type) {

		// Add Multiple Friends
		case *pb.FriendListenerRequest_AddFriendList:
			friendList := msg.AddFriendList.FriendIds
			log.Printf("📥 Received AddFriendList: %v", friendList)

			// Filter out friends that are already in the active list
			var newFriends []string
			for _, friendID := range friendList {
				if _, exists := activeFriends[friendID]; !exists {
					newFriends = append(newFriends, friendID)
					activeFriends[friendID] = struct{}{}
				}
			}

			if len(newFriends) > 0 {
				log.Printf("📡 Listening for updates from new friends: %v", newFriends)
				s.listenToFriendUpdates(stream, newFriends, activeFriends, pubsubMap)
			} else {
				log.Println("No new friends to add to the listener.")
			}

		// Add Single Friend
		case *pb.FriendListenerRequest_AddFriend:
			friendID := msg.AddFriend.FriendId
			log.Printf("📥 Received AddFriend: %s", friendID)

			if _, exists := activeFriends[friendID]; !exists {
				activeFriends[friendID] = struct{}{}
				log.Printf("📡 Listening for updates from new friend: %s", friendID)
				s.listenToFriendUpdates(stream, []string{friendID}, activeFriends, pubsubMap)
			} else {
				log.Printf("Friend %s is already being tracked", friendID)
			}

		// Remove Multiple Friends
		case *pb.FriendListenerRequest_RemoveFriendList:
			friendList := msg.RemoveFriendList.FriendIds
			log.Printf("📥 Received RemoveFriendList: %v", friendList)

			for _, friendID := range friendList {
				if _, exists := activeFriends[friendID]; exists {
					delete(activeFriends, friendID)
					if pubsub, exists := pubsubMap[friendID]; exists {
						pubsub.Close()
						delete(pubsubMap, friendID)
					}
					log.Printf("🗑️ Removed friend from active list: %s", friendID)
				}
			}

		// Remove Single Friend
		case *pb.FriendListenerRequest_RemoveFriend:
			friendID := msg.RemoveFriend.FriendId
			log.Printf("📥 Received RemoveFriend: %s", friendID)

			if _, exists := activeFriends[friendID]; exists {
				delete(activeFriends, friendID)
				if pubsub, exists := pubsubMap[friendID]; exists {
					pubsub.Close()
					delete(pubsubMap, friendID)
				}
				log.Printf("🗑️ Removed friend from active list: %s", friendID)
			}

		// Handle KeepAlive Acknowledgment
		case *pb.FriendListenerRequest_KeepaliveAck:
			ack := msg.KeepaliveAck
			log.Printf("Received KeepAliveAck from client: %v", ack.Message)

		default:
			log.Printf("Unknown message type received from client: %v", clientMsg)
		}
	}
}

func (s *Server) listenToFriendUpdates(
	stream pb.Server_FriendListenerServer,
	newFriends []string, // Renamed from friendIDs to newFriends
	activeFriends map[string]struct{},
	pubsubMap map[string]*redis.PubSub,
) {
	ctx := stream.Context()

	for _, friendID := range newFriends {
		// If the friend is already being tracked, skip them
		if _, exists := pubsubMap[friendID]; exists {
			log.Printf("Friend %s is already being tracked, skipping.", friendID)
			continue
		}

		// Get the friend's current status from Redis
		redisKey := "user:" + friendID
		status, err := s.redisClient.Get(ctx, redisKey).Result()
		if err != nil && err != redis.Nil {
			log.Printf("Error retrieving status for friend %s: %v", friendID, err)
			continue
		}

		statusEnum := s.mapStatusToEnum(status)
		log.Printf("Initial status for friend %s: %v", friendID, statusEnum)

		// Send the initial status update to the client
		err = stream.Send(&pb.FriendListenerResponse{
			Message: &pb.FriendListenerResponse_FriendUpdate{
				FriendUpdate: &pb.FriendUpdate{
					ClientId: friendID,
					Status:   statusEnum,
				},
			},
		})
		if err != nil {
			log.Printf("Failed to send initial status for friend %s: %v", friendID, err)
			continue
		}

		// Subscribe to the Redis Pub/Sub channel for this friend's status updates
		channel := fmt.Sprintf("status_updates:%s", friendID)
		pubsub := s.redisClient.Subscribe(ctx, channel)

		pubsubMap[friendID] = pubsub // Store the PubSub reference to later unsubscribe

		go func(friendID string, pubsub *redis.PubSub) {
			defer func() {
				log.Printf("Stopped listening to updates for friend %s", friendID)
			}()

			for msg := range pubsub.Channel() {
				statusEnum := s.mapStatusToEnum(msg.Payload)

				// Send the status update back to the client
				err := stream.Send(&pb.FriendListenerResponse{
					Message: &pb.FriendListenerResponse_FriendUpdate{
						FriendUpdate: &pb.FriendUpdate{
							ClientId: friendID,
							Status:   statusEnum,
						},
					},
				})
				if err != nil {
					log.Printf("Failed to send status update for friend %s: %v", friendID, err)
					break // If send fails, break out of the loop
				}

				log.Printf("Sent status update for friend %s: %v", friendID, statusEnum)
			}

			// Clean up Redis Pub/Sub subscription
			pubsub.Close()
			delete(pubsubMap, friendID)
			delete(activeFriends, friendID)
		}(friendID, pubsub)
	}
}

func (s *Server) mapStatusToEnum(status string) pb.FriendUpdate_Status {
	switch status {
	case "foreground":
		return pb.FriendUpdate_FOREGROUND
	case "background":
		return pb.FriendUpdate_BACKGROUND
	case "offline":
		return pb.FriendUpdate_OFFLINE
	default:
		log.Printf("Unknown status '%s' received, defaulting to OFFLINE", status)
		return pb.FriendUpdate_OFFLINE
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

	log.Printf("Server is running on port 50051: Upgrade Pong Structure")
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
