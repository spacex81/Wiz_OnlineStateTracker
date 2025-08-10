# Wiz Online State Tracker

Real-time friend status tracking service for the Wiz iOS walkie-talkie app, built with Go and gRPC.

## Overview

A microservice that monitors online/offline status of friends with 1-second ping intervals. Uses gRPC bidirectional streaming and Redis pub/sub for real-time status updates across thousands of concurrent connections.

## Architecture

**Tech Stack**: Go 1.23, gRPC, Redis 6.x, AWS ECS Fargate

**Core System**: 
- **Ping/Pong Service**: Server sends ping every second, client responds with status (`FOREGROUND`/`BACKGROUND`/`OFFLINE`)
- **Friend Listener**: Redis pub/sub broadcasts status changes to subscribed friends
- **TTL Management**: Redis keys auto-expire after 5 seconds of inactivity

## Key Features

- **Real-time Updates**: Status changes propagated within 500ms
- **Concurrent Handling**: Supports 10,000+ simultaneous connections
- **Mobile-Optimized**: Graceful reconnection for network interruptions
- **Production Ready**: Deployed on AWS with TLS, monitoring, and auto-scaling

## Infrastructure

- **AWS ECS Fargate**: Serverless container orchestration
- **ElastiCache Redis**: Distributed state management and pub/sub
- **Application Load Balancer**: TLS termination and gRPC routing
- **Terraform**: Infrastructure as Code deployment

Built as backend infrastructure for a production iOS app, demonstrating expertise in concurrent systems, real-time communication, and cloud architecture.
