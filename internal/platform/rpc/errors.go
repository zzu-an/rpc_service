package rpc

import (
	"context"
	"errors"

	commonv1 "service_rpc/api/gen/common/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type FailureClass uint8

const (
	FailureClassUnspecified FailureClass = iota
	FailureClassBusiness
	FailureClassCanceled
	FailureClassDependencyTimeout
	FailureClassDependencyUnavailable
	FailureClassInternal
)

// PublicError 将内部 cause 与对外信息分离。Error() 故意只返回公开文本，日志若需要 cause
// 应在服务边界显式记录，不能让 gRPC 自动把 SQL/DSN/堆栈透传给 gateway。
type PublicError struct {
	grpcCode     codes.Code
	businessCode commonv1.ErrorCode
	message      string
	retryable    bool
	cause        error
}

func NewPublicError(grpcCode codes.Code, businessCode commonv1.ErrorCode, message string, retryable bool, cause error) *PublicError {
	return &PublicError{grpcCode: grpcCode, businessCode: businessCode, message: message, retryable: retryable, cause: cause}
}

func (e *PublicError) Error() string { return e.message }
func (e *PublicError) Unwrap() error { return e.cause }

// StatusError 是统一的出站错误门。未显式映射的错误一律变成通用 Internal，禁止“省事”透传。
func StatusError(err error) error {
	if err == nil {
		return nil
	}
	var public *PublicError
	if !errors.As(err, &public) {
		return status.Error(codes.Internal, "internal service error")
	}
	base := status.New(public.grpcCode, public.message)
	withDetail, detailErr := base.WithDetails(&commonv1.ErrorDetail{
		Code: public.businessCode, Message: public.message, Retryable: public.retryable,
	})
	if detailErr != nil {
		return base.Err()
	}
	return withDetail.Err()
}

// Classify 只使用 context/grpc code，不按字符串猜测。业务拒绝不应计入依赖熔断；
// DeadlineExceeded/Unavailable 才代表依赖故障，调用方再结合幂等性决定是否重试。
func Classify(err error) FailureClass {
	if err == nil {
		return FailureClassUnspecified
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return FailureClassCanceled
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.FailedPrecondition,
		codes.Unauthenticated, codes.PermissionDenied, codes.ResourceExhausted:
		return FailureClassBusiness
	case codes.DeadlineExceeded:
		return FailureClassDependencyTimeout
	case codes.Unavailable:
		return FailureClassDependencyUnavailable
	default:
		return FailureClassInternal
	}
}
