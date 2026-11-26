package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/backup"
	"github.com/Humbarrt/kicker/server/internal/emergency"
)

// MaxRestoreUpload limits a restore upload.
const MaxRestoreUpload = 4 << 30

// BackupsView is GET /api/backups: the cycle, the locations, the list (spec 10 §2).
type BackupsView struct {
	Config   backup.Config    `json:"config"`
	Status   backup.Status    `json:"status"`
	Backups  []backup.Entry   `json:"backups"`
	AutoTime string           `json:"auto_time"` // the time of the automatic setting
	NextRun  *time.Time       `json:"next_run"`
	Drives   []string         `json:"drives"`  // USB drives that can be a location
	Local    string           `json:"local"`   // the kicker's own folder
	Pending  *backup.Manifest `json:"pending"` // a restore waiting for the restart
	Problem  bool             `json:"problem"`
}

// RestoreView answers a staged restore.
type RestoreView struct {
	Manifest   backup.Manifest `json:"manifest"`
	Restarting bool            `json:"restarting"` // false: restart the server by hand
}

func (o Options) backupsView(c *gin.Context) (BackupsView, error) {
	ctx := c.Request.Context()
	b := o.Backups
	cfg, err := b.Config(ctx)
	if err != nil {
		return BackupsView{}, err
	}
	st, err := b.Status(ctx)
	if err != nil {
		return BackupsView{}, err
	}
	p, err := backup.Pending(o.DataDir)
	if err != nil {
		return BackupsView{}, err
	}
	list, err := b.List(ctx)
	if err != nil {
		return BackupsView{}, err
	}
	return BackupsView{Config: cfg, Status: st, Backups: list, AutoTime: b.AutoTime(), NextRun: b.NextRun(cfg), Drives: b.Drives(),
		Local: b.Dir(backup.Location{Kind: backup.LocLocal}), Pending: p, Problem: b.Problem(ctx)}, nil
}

// restore stages an uploaded backup and restarts the server to swap it in (spec 10 §2). It is
// used by Settings → Backups and by step 2 of the wizard.
func (o Options) restore(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxRestoreUpload)
	file, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_input", "the backup file is missing")
		return
	}
	f, err := file.Open()
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	defer f.Close()
	m, err := backup.Stage(c.Request.Context(), o.DataDir, f, c.PostForm("passphrase"))
	o.restored(c, m, err)
}

// restored answers a staged restore and restarts the server to swap it in.
func (o Options) restored(c *gin.Context, m backup.Manifest, err error) {
	switch {
	case errors.Is(err, backup.ErrPassphrase):
		fail(c, http.StatusBadRequest, "wrong_passphrase", err.Error())
		return
	case errors.Is(err, backup.ErrNotFound):
		fail(c, http.StatusNotFound, "not_found", err.Error())
		return
	case err != nil:
		fail(c, http.StatusBadRequest, "bad_backup", err.Error())
		return
	}
	o.audit(c, "backup_restore_staged", m.Name, m.Created.Format(time.RFC3339))
	c.JSON(http.StatusOK, RestoreView{Manifest: m, Restarting: o.Restart != nil})
	if o.Restart != nil {
		c.Writer.Flush()
		o.Restart()
	}
}

// backupRoutes: Settings → Backups and the backup passphrase (Admin only, spec 10 §7).
func (o Options) backupRoutes(api *gin.RouterGroup) {
	b := o.Backups
	g := api.Group("/backups", RequireAdmin)
	g.GET("", func(c *gin.Context) {
		v, err := o.backupsView(c)
		if err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		c.JSON(http.StatusOK, v)
	})
	g.PUT("/config", func(c *gin.Context) {
		var cfg backup.Config
		if !bind(c, &cfg) {
			return
		}
		if err := b.SetConfig(c.Request.Context(), cfg); errors.Is(err, backup.ErrConfig) {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		} else if err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		o.audit(c, "backup_config_changed", "", "")
		v, _ := o.backupsView(c)
		c.JSON(http.StatusOK, v)
	})
	g.POST("/run", func(c *gin.Context) {
		if _, err := b.RunNow(c.Request.Context()); err != nil {
			fail(c, http.StatusInternalServerError, "backup_failed", err.Error())
			return
		}
		v, _ := o.backupsView(c)
		c.JSON(http.StatusOK, v)
	})
	g.GET("/download", func(c *gin.Context) {
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, b.Filename()))
		if _, err := b.Download(c.Request.Context(), c.Writer); err != nil {
			// Headers are gone already: the browser gets a broken file, the log says why.
			_ = c.Error(err)
		}
	})
	g.POST("/restore", o.restore)
	// Stored backups: the list's actions. A restore without a passphrase tries the current one.
	g.GET("/files/:file", func(c *gin.Context) {
		p, err := b.Path(c.Request.Context(), c.Param("file"), c.Query("location"))
		if err != nil {
			fail(c, http.StatusNotFound, "not_found", err.Error())
			return
		}
		c.FileAttachment(p, c.Param("file"))
	})
	g.DELETE("/files/:file", func(c *gin.Context) {
		if err := b.Delete(c.Request.Context(), c.Param("file"), c.Query("location")); errors.Is(err, backup.ErrNotFound) {
			fail(c, http.StatusNotFound, "not_found", err.Error())
			return
		} else if err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		o.audit(c, "backup_deleted", c.Param("file"), c.Query("location"))
		v, _ := o.backupsView(c)
		c.JSON(http.StatusOK, v)
	})
	g.POST("/files/:file/restore", func(c *gin.Context) {
		var in struct {
			Passphrase string `json:"passphrase"`
		}
		_ = c.ShouldBindJSON(&in)
		m, err := b.Restore(c.Request.Context(), c.Param("file"), in.Passphrase)
		o.restored(c, m, err)
	})
	// An uploaded backup joins the list (the kicker's own location); restoring it is a second step.
	g.POST("/upload", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, backup.MaxUpload+1<<20)
		file, err := c.FormFile("file")
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", "the backup file is missing")
			return
		}
		f, err := file.Open()
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		defer f.Close()
		name, err := b.Upload(f, file.Filename)
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		o.audit(c, "backup_uploaded", name, "")
		c.JSON(http.StatusOK, gin.H{"file": name})
	})
	g.POST("/passphrase", func(c *gin.Context) {
		var in struct {
			Password   string `json:"password"`   // re-entered, like for a new emergency kit
			Passphrase string `json:"passphrase"` // own; "" = new words
		}
		if !bind(c, &in) {
			return
		}
		if err := o.Auth.VerifyPassword(c.Request.Context(), identity(c), in.Password, c.ClientIP()); err != nil {
			authError(c, err)
			return
		}
		p, err := emergency.SetPassphrase(o.DataDir, in.Passphrase)
		if errors.Is(err, emergency.ErrShortPassphrase) {
			fail(c, http.StatusBadRequest, "short_passphrase", err.Error())
			return
		} else if err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		o.audit(c, "backup_passphrase_changed", "", "")
		c.JSON(http.StatusOK, gin.H{"passphrase": p})
	})
}
