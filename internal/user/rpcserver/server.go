// Package rpcserver 将 user/RBAC 用例适配为 Protobuf RPC，不包含 SQL 和 JWT 逻辑。
package rpcserver

import (
	"context"
	"errors"

	commonv1 "service_rpc/api/gen/common/v1"
	userv1 "service_rpc/api/gen/user/v1"
	platformrpc "service_rpc/internal/platform/rpc"
	"service_rpc/internal/rbac"
	"service_rpc/internal/user"

	"google.golang.org/grpc/codes"
)

type Server struct {
	userv1.UnimplementedUserServiceServer
	registration   *user.Service
	authentication *user.AuthService
	authorization  *rbac.Service
}

func New(registration *user.Service, authentication *user.AuthService, authorization *rbac.Service) *Server {
	return &Server{registration: registration, authentication: authentication, authorization: authorization}
}

func (s *Server) Register(ctx context.Context, request *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	created, err := s.registration.Register(ctx, request.GetEmail(), request.GetPassword())
	if err != nil {
		return nil, mapError(err)
	}
	return &userv1.RegisterResponse{User: toProtoUser(created)}, nil
}

func (s *Server) Authenticate(ctx context.Context, request *userv1.AuthenticateRequest) (*userv1.AuthenticateResponse, error) {
	// 凭据只在本次调用内传给 bcrypt，绝不能进入日志、错误 detail 或响应。
	// 面试点：未知账号同样计算 dummy hash，降低通过响应耗时枚举账号的信号。
	authenticated, err := s.authentication.Authenticate(ctx, request.GetEmail(), request.GetPassword())
	if err != nil {
		return nil, mapError(err)
	}
	roles, err := s.authorization.UserRoles(ctx, authenticated.ID)
	if err != nil {
		return nil, mapError(err)
	}
	return &userv1.AuthenticateResponse{User: toProtoUser(authenticated), RoleCodes: roles}, nil
}

func (s *Server) GetUser(ctx context.Context, request *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	found, err := s.authentication.CurrentUser(ctx, request.GetUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &userv1.GetUserResponse{User: toProtoUser(found)}, nil
}

func (s *Server) GetUserRoles(ctx context.Context, request *userv1.GetUserRolesRequest) (*userv1.GetUserRolesResponse, error) {
	roles, err := s.authorization.UserRoles(ctx, request.GetUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &userv1.GetUserRolesResponse{RoleCodes: roles}, nil
}

func (s *Server) HasPermission(ctx context.Context, request *userv1.HasPermissionRequest) (*userv1.HasPermissionResponse, error) {
	// gateway 传入 user_id 只是“已完成边缘认证”的身份事实，并不代表业务服务应盲信任意权限结论。
	// 权限仍由 user-rpc 查询当前 RBAC 状态，角色变更才能在下一次请求生效。
	allowed, err := s.authorization.HasPermission(ctx, request.GetUserId(), request.GetPermissionCode())
	if err != nil {
		return nil, mapError(err)
	}
	return &userv1.HasPermissionResponse{Allowed: allowed}, nil
}

func (s *Server) ReplaceUserRoles(ctx context.Context, request *userv1.ReplaceUserRolesRequest) (*userv1.ReplaceUserRolesResponse, error) {
	if err := s.authorization.ReplaceUserRoles(ctx, request.GetUserId(), request.GetRoleCodes()); err != nil {
		return nil, mapError(err)
	}
	return &userv1.ReplaceUserRolesResponse{}, nil
}

func toProtoUser(value user.User) *userv1.User {
	return &userv1.User{Id: value.ID, Email: value.Email, CreatedAtMs: value.CreatedAt.UTC().UnixMilli()}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Canceled,
			commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "request canceled", true, err))
	case errors.Is(err, context.DeadlineExceeded):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.DeadlineExceeded,
			commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "user service timeout", true, err))
	case errors.Is(err, user.ErrInvalidEmail), errors.Is(err, user.ErrInvalidPassword):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.InvalidArgument,
			commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid registration", false, err))
	case errors.Is(err, user.ErrUserAlreadyExists):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.AlreadyExists,
			commonv1.ErrorCode_ERROR_CODE_CONFLICT, "user already exists", false, err))
	case errors.Is(err, user.ErrInvalidCredentials):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Unauthenticated,
			commonv1.ErrorCode_ERROR_CODE_UNAUTHENTICATED, "invalid credentials", false, err))
	case errors.Is(err, user.ErrUserNotFound), errors.Is(err, rbac.ErrUserNotFound):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.NotFound,
			commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, "user not found", false, err))
	case errors.Is(err, rbac.ErrRoleAssignmentConflict):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.FailedPrecondition,
			commonv1.ErrorCode_ERROR_CODE_CONFLICT, "role assignment conflict", false, err))
	default:
		return platformrpc.StatusError(err)
	}
}
