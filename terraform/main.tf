provider "aws" {
  region = "ap-northeast-2"
}

# -------------------------------
# Variables 
# -------------------------------
variable "vpc_cidr" {
  description = "The CIDR block for the VPC"
  default     = "10.0.0.0/16"
}

variable "public_subnet_cidrs" {
  description = "List of CIDR blocks for the public subnets"
  default     = ["10.0.1.0/24", "10.0.2.0/24"]
}

variable "private_subnet_cidrs" {
  description = "List of CIDR blocks for the private subnets"
  default     = ["10.0.3.0/24", "10.0.4.0/24"]
}

variable "domain_name" {
  description = "The domain name (e.g., komaki.tech)"
  default     = "komaki.tech"
}

variable "route53_zone_id" {
  description = "The Route 53 Hosted Zone ID for the domain"
  default     = "Z00629463ECRS5LIGB3FP"
}

variable "acm_certificate_arn" {
  description = "ARN of the ACM Certificate"
  default     = "arn:aws:acm:ap-northeast-2:784319439312:certificate/62cf07b5-ed90-45ba-8f37-6646958acd19"
}

variable "allowed_cidr_blocks" {
  description = "List of CIDR blocks that are allowed to access the ALB (0.0.0.0/0 for public access)"
  default     = ["0.0.0.0/0"]
}

variable "health_check_path" {
  description = "Path to be used for health checks"
  default     = "/health"
}

# -------------------------------
# CloudWatch
# -------------------------------
resource "aws_cloudwatch_log_group" "ecs_logs" {
  name              = "/ecs/friend-tracker"
  retention_in_days = 7  # Number of days to retain logs (change as needed)
}

resource "aws_iam_role" "ecs_task_execution_role" {
  name = "ecsTaskExecutionRole"

  assume_role_policy = jsonencode({
    Version = "2012-10-17",
    Statement = [{
      Effect = "Allow",
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      },
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_policy_attachment" "ecs_task_execution_policy_attachment" {
  name       = "ecs-task-execution-policy-attachment"
  roles      = [aws_iam_role.ecs_task_execution_role.name]
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# -------------------------------
# VPC, Public & Private Subnets
# -------------------------------
resource "aws_vpc" "main" {
  cidr_block = var.vpc_cidr

  tags = {
    Name = "main-vpc"
  }
}

resource "aws_subnet" "public" {
  for_each = toset(var.public_subnet_cidrs)

  vpc_id            = aws_vpc.main.id
  cidr_block        = each.value
  map_public_ip_on_launch = true

  tags = {
    Name = "public-subnet-${each.value}"
  }
}

resource "aws_subnet" "private" {
  for_each = toset(var.private_subnet_cidrs)

  vpc_id     = aws_vpc.main.id
  cidr_block = each.value

  tags = {
    Name = "private-subnet-${each.value}"
  }
}

# -------------------------------
# Internet Gateway & NAT Gateway 
# -------------------------------
resource "aws_internet_gateway" "igw" {
  vpc_id = aws_vpc.main.id

}

resource "aws_eip" "nat_eip" {
#   vpc = true
  domain = "vpc"
}

resource "aws_nat_gateway" "nat" {
  allocation_id = aws_eip.nat_eip.id
  subnet_id     = aws_subnet.public["10.0.1.0/24"].id 
}

# -------------------------------
# Route Tables 
# -------------------------------
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw.id
  }
}

resource "aws_route_table_association" "public" {
  for_each = aws_subnet.public

  subnet_id      = each.value.id
  route_table_id = aws_route_table.public.id
}

# Route table for private subnets
resource "aws_route_table" "private" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.nat.id
  }
}

resource "aws_route_table_association" "private" {
  for_each = aws_subnet.private

  subnet_id      = each.value.id
  route_table_id = aws_route_table.private.id
}
# -------------------------------
# Security Groups
# -------------------------------
resource "aws_security_group" "alb_sg" {
  vpc_id = aws_vpc.main.id

  ingress {
    description      = "Allow HTTPS traffic"
    from_port        = 443
    to_port          = 443
    protocol         = "tcp"
    cidr_blocks      = var.allowed_cidr_blocks
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"] 
  }
}

resource "aws_security_group" "ecs_sg" {
  vpc_id = aws_vpc.main.id

  ingress {
    from_port        = 50051  
    to_port          = 50051  
    protocol         = "tcp"
    security_groups  = [aws_security_group.alb_sg.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"] 
  }
}

# -------------------------------
# ECS Fargate Resources
# -------------------------------
resource "aws_ecs_cluster" "main" {
  name = "server-cluster"
}

resource "aws_ecs_task_definition" "server_task" {
  family                = "friend-tracker-task"
  network_mode          = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                   = "256"
  memory                = "512"

  execution_role_arn = aws_iam_role.ecs_task_execution_role.arn

  container_definitions = jsonencode([{
    name      = "friend-tracker"
    image     = "spacex81359/friend-tracker2:latest"
    essential = true
    portMappings = [{
      containerPort = 50051  # Updated to 50051
      hostPort      = 50051  # Updated to 50051
    }]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = "/ecs/friend-tracker"
        awslogs-region        = "ap-northeast-2"
        awslogs-stream-prefix = "ecs"
      }
    }
  }])
}

resource "aws_ecs_service" "server_service" {
  name               = "friend-tracker-ecs"
  cluster            = aws_ecs_cluster.main.id
  desired_count      = 1
  launch_type        = "FARGATE"
  task_definition    = aws_ecs_task_definition.server_task.arn

  load_balancer {
    target_group_arn = aws_lb_target_group.server.arn
    container_name   = "friend-tracker"
    container_port   = 50051  # Updated to 50051
  }

  network_configuration {
    subnets         = [for subnet in aws_subnet.private : subnet.id]
    security_groups = [aws_security_group.ecs_sg.id]
  }
}

# -------------------------------
# Load Balancer & Listeners
# -------------------------------
resource "aws_lb" "server_alb" {
  name               = "server-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb_sg.id]
  subnets            = [for subnet in aws_subnet.public : subnet.id]

  enable_deletion_protection = false
}

resource "aws_lb_target_group" "server" {
  name         = "server-target-group"
  port         = 50051
  protocol     = "HTTP"
  vpc_id       = aws_vpc.main.id
  target_type  = "ip" 

  health_check {
    path                = var.health_check_path
    port                = "50051"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 2
  }

  tags = {
    Name = "server-target-group"
  }
}

resource "aws_lb_listener" "server_https" {
  load_balancer_arn = aws_lb.server_alb.arn
  port              = 443
  protocol          = "HTTPS"
  certificate_arn   = var.acm_certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.server.arn
  }
}

# -------------------------------
# Route 53 DNS
# -------------------------------
resource "aws_route53_record" "alias" {
  zone_id = var.route53_zone_id
  name    = var.domain_name
  type    = "A"

  alias {
    name                   = aws_lb.server_alb.dns_name
    zone_id                = aws_lb.server_alb.zone_id
    evaluate_target_health = true
  }
}
