package device

import (
	"context"
	"time"
)

// IDeviceUsecase defines device lifecycle operations.
type IDeviceUsecase interface {
	ListDevices(ctx context.Context) ([]Device, error)
	GetDevice(ctx context.Context, deviceID string) (*Device, error)
	AddDevice(ctx context.Context, deviceID string) (*Device, error)
	RemoveDevice(ctx context.Context, deviceID string) error
	LoginDevice(ctx context.Context, deviceID string) (LoginResponse, error)
	LoginDeviceWithCode(ctx context.Context, deviceID string, phone string) (string, error)
	LogoutDevice(ctx context.Context, deviceID string) error
	ReconnectDevice(ctx context.Context, deviceID string) error
	GetStatus(ctx context.Context, deviceID string) (isConnected bool, isLoggedIn bool, err error)
}

type LoginResponse struct {
	ImagePath string        `json:"image_path"`
	Duration  time.Duration `json:"duration"`
	Code      string        `json:"code"`
}
