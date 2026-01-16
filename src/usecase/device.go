package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/config"
	domainDevice "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/device"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/ui/websocket"
	fiberUtils "github.com/gofiber/fiber/v2/utils"
	"github.com/sirupsen/logrus"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
)

type serviceDevice struct {
	manager *whatsapp.DeviceManager
}

func NewDeviceService(manager *whatsapp.DeviceManager) domainDevice.IDeviceUsecase {
	return &serviceDevice{
		manager: manager,
	}
}

func (s *serviceDevice) ListDevices(_ context.Context) ([]domainDevice.Device, error) {
	if s.manager == nil {
		return []domainDevice.Device{}, nil
	}

	var result []domainDevice.Device
	for _, inst := range s.manager.ListDevices() {
		inst.UpdateStateFromClient()
		result = append(result, convertInstance(inst))
	}
	return result, nil
}

func (s *serviceDevice) GetDevice(_ context.Context, deviceID string) (*domainDevice.Device, error) {
	if s.manager == nil {
		return nil, fmt.Errorf("device manager not initialized")
	}
	if inst, ok := s.manager.GetDevice(deviceID); ok {
		device := convertInstance(inst)
		return &device, nil
	}
	return nil, fmt.Errorf("device %s not found", deviceID)
}

func (s *serviceDevice) AddDevice(ctx context.Context, deviceID string) (*domainDevice.Device, error) {
	if s.manager == nil {
		return nil, fmt.Errorf("device manager not initialized")
	}

	inst, err := s.manager.CreateDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	device := convertInstance(inst)
	return &device, nil
}

func (s *serviceDevice) RemoveDevice(_ context.Context, deviceID string) error {
	if s.manager == nil {
		return fmt.Errorf("device manager not initialized")
	}
	s.manager.RemoveDevice(deviceID)
	return nil
}

func (s *serviceDevice) LoginDevice(ctx context.Context, deviceID string) (response domainDevice.LoginResponse, err error) {
	if s.manager == nil {
		return response, fmt.Errorf("device manager not initialized")
	}

	// Ensure device has a WhatsApp client initialized
	inst, err := s.manager.EnsureClient(ctx, deviceID)
	if err != nil {
		return response, fmt.Errorf("failed to ensure client for device %s: %w", deviceID, err)
	}

	// Get WhatsApp client
	client := inst.GetClient()
	if client == nil {
		return response, fmt.Errorf("WhatsApp client not initialized for device %s", deviceID)
	}

	// Check if already logged in
	if client.IsLoggedIn() {
		inst.UpdateStateFromClient()
		return response, pkgError.ErrAlreadyLoggedIn
	}

	// Disconnect first to ensure QR flow starts cleanly
	client.Disconnect()

	chImage := make(chan string, 1) // Buffered to prevent goroutine leak
	// Use background context for QR channel - the WhatsApp connection must persist
	// beyond the HTTP request lifetime
	qrCtx := context.Background()
	ch, err := client.GetQRChannel(qrCtx)
	if err != nil {
		logrus.Errorf("[LOGIN][%s] GetQRChannel failed: %v", deviceID, err)
		if errors.Is(err, whatsmeow.ErrQRStoreContainsID) {
			_ = client.Connect()
			inst.UpdateStateFromClient()
			if client.IsLoggedIn() {
				return response, pkgError.ErrAlreadyLoggedIn
			}
			return response, pkgError.ErrSessionSaved
		}
		return response, pkgError.ErrQrChannel
	}

	go func() {
		defer close(chImage) // Ensure channel is closed when done
		for evt := range ch {
			response.Code = evt.Code
			response.Duration = evt.Timeout / time.Second / 2
			if evt.Event == "code" {
				qrPath := fmt.Sprintf("%s/scan-qr-%s.png", config.PathQrCode, fiberUtils.UUIDv4())
				if err := qrcode.WriteFile(evt.Code, qrcode.Medium, 512, qrPath); err != nil {
					logrus.Errorf("[LOGIN][%s] Error when write qr code to file: %v", deviceID, err)
					continue // Skip sending if QR generation failed
				}
				go func(path string, duration time.Duration) {
					time.Sleep(duration * time.Second)
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						logrus.Errorf("[LOGIN][%s] error when remove qrImage file: %v", deviceID, err)
					}
				}(qrPath, response.Duration)
				// Use select to avoid blocking if QR context is canceled
				select {
				case chImage <- qrPath:
				case <-qrCtx.Done():
					logrus.Warnf("[LOGIN][%s] QR context canceled while sending QR path", deviceID)
					return
				}
			} else {
				logrus.Errorf("[LOGIN][%s] error when get qrCode %s %v", deviceID, evt.Event, evt.Error)
			}
		}
	}()

	if err = client.Connect(); err != nil {
		return response, fmt.Errorf("failed to connect: %w", err)
	}

	inst.UpdateStateFromClient()

	// Wait for QR image with timeout to prevent hanging
	select {
	case imagePath, ok := <-chImage:
		if !ok {
			return response, fmt.Errorf("QR channel closed without receiving image")
		}
		response.ImagePath = imagePath
	case <-ctx.Done():
		return response, ctx.Err()
	case <-time.After(120 * time.Second):
		return response, fmt.Errorf("timeout waiting for QR code")
	}

	return response, nil
}

func (s *serviceDevice) LoginDeviceWithCode(_ context.Context, _ string, _ string) (string, error) {
	return "", fmt.Errorf("device login with code is not implemented yet")
}

func (s *serviceDevice) LogoutDevice(ctx context.Context, deviceID string) error {
	if s.manager == nil {
		return fmt.Errorf("device manager not initialized")
	}

	if err := s.manager.PurgeDevice(ctx, deviceID); err != nil {
		return err
	}

	// Broadcast device removal so UI clients can refresh.
	var devices []domainDevice.Device
	if s.manager != nil {
		for _, inst := range s.manager.ListDevices() {
			inst.UpdateStateFromClient()
			devices = append(devices, convertInstance(inst))
		}
	}

	websocket.Broadcast <- websocket.BroadcastMessage{
		Code:    "DEVICE_REMOVED",
		Message: fmt.Sprintf("Device %s logged out and removed", deviceID),
		Result: map[string]any{
			"device_id": deviceID,
			"devices":   devices,
		},
	}

	return nil
}

func (s *serviceDevice) ReconnectDevice(_ context.Context, deviceID string) error {
	if s.manager == nil {
		return fmt.Errorf("device manager not initialized")
	}
	if inst, ok := s.manager.GetDevice(deviceID); ok {
		client := inst.GetClient()
		if client == nil {
			return fmt.Errorf("device %s client not initialized", deviceID)
		}

		if client.Store == nil || client.Store.ID == nil {
			return fmt.Errorf("device %s is not logged in (session deleted)", deviceID)
		}

		client.Disconnect()
		return client.Connect()
	}
	return fmt.Errorf("device %s not found", deviceID)
}

func (s *serviceDevice) GetStatus(_ context.Context, deviceID string) (bool, bool, error) {
	if s.manager == nil {
		return false, false, fmt.Errorf("device manager not initialized")
	}
	if inst, ok := s.manager.GetDevice(deviceID); ok {
		inst.UpdateStateFromClient()
		client := inst.GetClient()
		if client == nil {
			return false, false, nil
		}

		if client.Store == nil || client.Store.ID == nil {
			return false, false, nil
		}

		// Update state snapshot based on live client flags
		state := deriveState(inst)
		_ = state
		return client.IsConnected(), client.IsLoggedIn(), nil
	}
	return false, false, fmt.Errorf("device %s not found", deviceID)
}

func convertInstance(inst *whatsapp.DeviceInstance) domainDevice.Device {
	if inst == nil {
		return domainDevice.Device{}
	}

	state := deriveState(inst)

	return domainDevice.Device{
		ID:          inst.ID(),
		PhoneNumber: inst.PhoneNumber(),
		DisplayName: inst.DisplayName(),
		State:       state,
		JID:         inst.JID(),
		CreatedAt:   inst.CreatedAt(),
	}
}

func deriveState(inst *whatsapp.DeviceInstance) domainDevice.DeviceState {
	if inst == nil {
		return domainDevice.DeviceStateDisconnected
	}

	client := inst.GetClient()
	state := inst.State()
	if client != nil {
		if client.IsLoggedIn() {
			state = domainDevice.DeviceStateLoggedIn
		} else if client.IsConnected() {
			state = domainDevice.DeviceStateConnected
		} else {
			state = domainDevice.DeviceStateDisconnected
		}
		inst.SetState(state)
	}

	return state
}
