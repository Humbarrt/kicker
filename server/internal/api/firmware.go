package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/firmware"
)

// MaxFirmwareUpload limits an uploaded .uf2 (an RP2350 has 16 MB flash at most; UF2 doubles it).
const MaxFirmwareUpload = 64 << 20

// FlashDriveInput flashes a module waiting in its bootloader with a file of the release.
type FlashDriveInput struct {
	File string `json:"file" binding:"required"`
}

// UpdateFirmwareInput starts firmware updates, one module after the other.
type UpdateFirmwareInput struct {
	UIDs []string `json:"uids" binding:"required,min=1"`
}

func firmwareError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, firmware.ErrBusy):
		fail(c, http.StatusConflict, "busy", err.Error())
	case errors.Is(err, firmware.ErrQueued):
		fail(c, http.StatusConflict, "queued", err.Error())
	case errors.Is(err, firmware.ErrNoOffer), errors.Is(err, firmware.ErrNoModule), errors.Is(err, firmware.ErrNoDrive):
		fail(c, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, firmware.ErrOffline):
		fail(c, http.StatusConflict, "offline", err.Error())
	case errors.Is(err, firmware.ErrNoUF2), errors.Is(err, firmware.ErrUF2):
		fail(c, http.StatusBadRequest, "bad_firmware", err.Error())
	default:
		fail(c, http.StatusInternalServerError, "internal", err.Error())
	}
}

// firmwareRoutes: module firmware updates (Admin only, spec 10 §4).
func (o Options) firmwareRoutes(api *gin.RouterGroup) {
	f := o.Firmware
	g := api.Group("/firmware", RequireAdmin)
	status := func(c *gin.Context) { c.JSON(http.StatusOK, f.Status(c.Request.Context())) }
	g.GET("", status)
	g.POST("/update", func(c *gin.Context) {
		var in UpdateFirmwareInput
		if !bind(c, &in) {
			return
		}
		if err := f.Update(c.Request.Context(), in.UIDs); err != nil {
			firmwareError(c, err)
			return
		}
		o.audit(c, "firmware_update", "", "")
		status(c)
	})
	readFile := func(c *gin.Context) (string, []byte, bool) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxFirmwareUpload)
		fh, err := c.FormFile("file")
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", "the .uf2 file is missing")
			return "", nil, false
		}
		r, err := fh.Open()
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return "", nil, false
		}
		defer r.Close()
		data, err := io.ReadAll(r)
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return "", nil, false
		}
		return fh.Filename, data, true
	}
	g.POST("/modules/:uid/upload", func(c *gin.Context) {
		name, data, ok := readFile(c)
		if !ok {
			return
		}
		if err := f.Upload(c.Request.Context(), c.Param("uid"), name, data); err != nil {
			firmwareError(c, err)
			return
		}
		o.audit(c, "firmware_upload", c.Param("uid"), name)
		status(c)
	})
	g.POST("/drives/:name/flash", func(c *gin.Context) {
		var in FlashDriveInput
		if !bind(c, &in) {
			return
		}
		if err := f.FlashDrive(c.Request.Context(), c.Param("name"), in.File, nil); err != nil {
			firmwareError(c, err)
			return
		}
		status(c)
	})
	g.POST("/drives/:name/upload", func(c *gin.Context) {
		name, data, ok := readFile(c)
		if !ok {
			return
		}
		if err := f.FlashDrive(c.Request.Context(), c.Param("name"), name, data); err != nil {
			firmwareError(c, err)
			return
		}
		o.audit(c, "firmware_upload", c.Param("name"), name)
		status(c)
	})
}
