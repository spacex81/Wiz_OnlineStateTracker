package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	pb "example.com/service"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// TODO: Need a data structure to represent friend list and each online state
var friendStatusMap sync.Map

func main() {
	var clientID string
	var friends []string

	// Handle command line arguments for clientID and friends list
	if len(os.Args) > 1 {
		clientID = os.Args[1]
		if len(os.Args) > 2 {
			friends = os.Args[2:] // All arguments after clientID are treated as friend IDs
		} else {
			log.Println("No friends provided.")
		}
	} else {
		clientID = uuid.New().String()
		log.Printf("No clientID provided. Generated UUID: %s", clientID)
		log.Println("No friends provided.")
	}

	log.Printf("Client ID: %s", clientID)
	log.Printf("Friend list: %v", friends)

	// Reconnection logic for Ping/Pong Client
	go func() {
		for {
			err := runPingPongClient(clientID)
			if err != nil {
				log.Printf("PingPong connection lost: %v. Reconnecting...", err)
				time.Sleep(2 * time.Second) // Wait before reconnecting
			}
		}
	}()

	// Reconnection logic for Friend Listener Client
	go func() {
		for {
			err := runFriendListenerClient(clientID, friends)
			if err != nil {
				log.Printf("Friend Listener connection lost: %v. Reconnecting...", err)
				time.Sleep(2 * time.Second) // Wait before reconnecting
			}
		}
	}()

	select {}
}

// runPingPongClient handles the ping/pong logic
func runPingPongClient(clientID string) error {
	log.Printf("Starting PingPong client for ClientID: %s", clientID)

	// conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	conn, err := dialServer()
	if err != nil {
		log.Printf("Failed to connect to gRPC server: %v", err)
		return err
	}
	defer conn.Close()

	client := pb.NewServerClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.Communicate(ctx)
	if err != nil {
		return err
	}

	// Send ClientHello message
	err = stream.Send(&pb.ClientMessage{
		Message: &pb.ClientMessage_ClientHello{
			ClientHello: &pb.ClientHello{ClientId: clientID},
		},
	})
	if err != nil {
		return err
	}

	// Goroutine to handle ping/pong messages
	go func() {
		for {
			_, err := stream.Recv()
			if err != nil {
				log.Printf("Error receiving ping: %v", err)
				cancel() // Cancel the context, forcing a reconnection
				return
			}

			err = stream.Send(&pb.ClientMessage{
				Message: &pb.ClientMessage_Pong{
					Pong: &pb.Pong{Status: pb.Pong_FOREGROUND}, // Example: Always return FOREGROUND for now
				},
			})
			if err != nil {
				log.Printf("Error sending pong: %v", err)
				cancel() // Cancel the context, forcing a reconnection
				return
			}
		}
	}()

	<-ctx.Done() // Wait until context is canceled
	return ctx.Err()
}

// runFriendListenerClient listens for friend status updates
func runFriendListenerClient(clientID string, friends []string) error {
	log.Printf("🚀 Starting Friend Listener client for ClientID: %s with friends: %v", clientID, friends)

	conn, err := dialServer()
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pb.NewServerClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Open the friend listener stream
	stream, err := client.FriendListener(ctx)
	if err != nil {
		return err
	}

	// Send the initial friend list as a FriendListenerRequest
	err = stream.Send(&pb.FriendListenerRequest{
		Message: &pb.FriendListenerRequest_FriendList{
			FriendList: &pb.FriendList{FriendIds: friends},
		},
	})
	if err != nil {
		log.Printf("❌ Failed to send FriendList: %v", err)
		return err
	}
	log.Println("✅ Sent FriendList successfully")

	// Goroutine to handle incoming FriendListenerResponse
	go func() {
		for {
			response, err := stream.Recv()
			if err != nil {
				log.Printf("❌ Error receiving FriendListenerResponse: %v", err)
				cancel() // Cancel the context, forcing a reconnection
				return
			}

			switch msg := response.Message.(type) {
			// Handle friend update
			case *pb.FriendListenerResponse_FriendUpdate:
				friendUpdate := msg.FriendUpdate
				log.Printf("📢 Friend update - ID: %s, Status: %v", friendUpdate.ClientId, friendUpdate.Status.String())
				updateFriendStatus(friendUpdate.ClientId, friendUpdate.Status)

			// Handle keepalive ping
			case *pb.FriendListenerResponse_KeepalivePing:
				// keepalivePing := msg.KeepalivePing
				// log.Printf("🔥 Received KeepAlivePing from server: %s", keepalivePing.Message)

				// Send back the KeepAliveAck
				err := stream.Send(&pb.FriendListenerRequest{
					Message: &pb.FriendListenerRequest_KeepaliveAck{
						KeepaliveAck: &pb.KeepAliveAck{
							Message: "ACK from Go client",
						},
					},
				})
				if err != nil {
					log.Printf("❌ Failed to send KeepAliveAck: %v", err)
				} else {
					// log.Println("✅ Sent KeepAliveAck successfully")
				}

			default:
				log.Printf("⚠️ Unknown message type received from server: %v", response)
			}
		}
	}()

	<-ctx.Done() // Wait until context is canceled
	return ctx.Err()
}

func updateFriendStatus(friendID string, status pb.FriendUpdate_Status) {
	var statusString string
	switch status {
	case pb.FriendUpdate_FOREGROUND:
		statusString = "FOREGROUND"
	case pb.FriendUpdate_BACKGROUND:
		statusString = "BACKGROUND"
	case pb.FriendUpdate_OFFLINE:
		statusString = "OFFLINE"
	default:
		statusString = "UNKNOWN"
	}

	friendStatusMap.Store(friendID, statusString)
	log.Printf("Updated friend %s to %s", friendID, statusString)
	printFriendStatusTable()
}

func printFriendStatusTable() {
	fmt.Println("====== Friend Status Table ======")
	friendStatusMap.Range(func(key, value interface{}) bool {
		clientID := key.(string)
		status := value.(string)
		fmt.Printf("Friend ID: %s | Status: %s\n", clientID, status)
		return true
	})
	fmt.Println("=================================")
}

// Shared function to dial the gRPC server
func dialServer() (*grpc.ClientConn, error) {
	log.Println("🔗 Dialing gRPC server at komaki.tech:443 ...")
	conn, err := grpc.Dial(
		"komaki.tech:443",
		grpc.WithTransportCredentials(credentials.NewTLS(nil)), // 👈 Secure TLS
		grpc.WithAuthority("komaki.tech"),                      // 👈 SNI for TLS
		grpc.WithBlock(),                                       // 👈 Block until connection is established
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(16*1024*1024),
			grpc.MaxCallSendMsgSize(16*1024*1024),
		),
	)
	// grpc.WithKeepaliveParams(keepalive.ClientParameters{
	// 	Time:    10 * time.Second,  // Send ping every 10 seconds if no activity
	// 	Timeout: 20 * time.Second, // Wait 20 seconds for ping ack before resetting
	// }),

	if err != nil {
		log.Printf("❌ Failed to connect to gRPC server: %v", err)
		return nil, err
	}
	log.Println("✅ Successfully connected to the gRPC server")
	return conn, nil
}
