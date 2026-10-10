package service

import (
	"context"
	"errors"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/render"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// GetTemplate returns a template of this kind; "" or "default" falls back to
// the built-in.
func (s *Service) GetTemplate(ctx context.Context, id string) (*model.Template, error) {
	if id == "" {
		id = render.DefaultTemplateID
	}
	t, err := s.palace.store.GetTemplate(ctx, s.kind.Name, id)
	if err == nil {
		t.Kind = s.kind.Name
		return t, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, storageErr(err)
	}
	if id == render.DefaultTemplateID {
		return &model.Template{Kind: s.kind.Name, ID: id, Content: render.DefaultTemplate(s.kind.Name)}, nil
	}
	return nil, newErr(KindNotFound, "%s template %q not found", s.kind.Name, id)
}

// ListTemplates lists stored templates of this kind plus the built-in default.
func (s *Service) ListTemplates(ctx context.Context) ([]*model.Template, error) {
	list, err := s.palace.store.ListTemplates(ctx, s.kind.Name)
	if err != nil {
		return nil, storageErr(err)
	}
	hasDefault := false
	for _, t := range list {
		t.Kind = s.kind.Name
		if t.ID == render.DefaultTemplateID {
			hasDefault = true
		}
	}
	if !hasDefault {
		list = append([]*model.Template{{Kind: s.kind.Name, ID: render.DefaultTemplateID, Content: render.DefaultTemplate(s.kind.Name)}}, list...)
	}
	return list, nil
}

func checkTemplateID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\ `) || id == "." || id == ".." {
		return newErr(KindBadRequest, "invalid template id %q", id)
	}
	return nil
}

// CreateTemplate stores a new template.
func (s *Service) CreateTemplate(ctx context.Context, id, content string) (*model.Template, error) {
	if err := checkTemplateID(id); err != nil {
		return nil, err
	}
	if err := render.Validate(content); err != nil {
		return nil, &Error{Kind: KindValidation, Message: "template does not parse", Problems: []string{err.Error()}}
	}
	if _, err := s.palace.store.GetTemplate(ctx, s.kind.Name, id); err == nil {
		return nil, newErr(KindConflict, "%s template %q already exists", s.kind.Name, id)
	}
	return s.putTemplate(ctx, &model.Template{Kind: s.kind.Name, ID: id, Content: content})
}

// UpdateTemplate replaces an existing template (the built-in default may be overridden).
func (s *Service) UpdateTemplate(ctx context.Context, id, content string) (*model.Template, error) {
	if err := checkTemplateID(id); err != nil {
		return nil, err
	}
	if err := render.Validate(content); err != nil {
		return nil, &Error{Kind: KindValidation, Message: "template does not parse", Problems: []string{err.Error()}}
	}
	if _, err := s.GetTemplate(ctx, id); err != nil {
		return nil, err
	}
	return s.putTemplate(ctx, &model.Template{Kind: s.kind.Name, ID: id, Content: content})
}

func (s *Service) putTemplate(ctx context.Context, t *model.Template) (*model.Template, error) {
	t.Updated = s.timestamp()
	err := s.palace.store.PutTemplate(ctx, t)
	var fe *store.FanoutError
	if errors.As(err, &fe) {
		return t, &Error{Kind: KindStorage, Message: fe.Error(), Partial: true, Cause: err}
	}
	if err != nil {
		return nil, storageErr(err)
	}
	s.palace.bus.Publish(events.Event{Type: events.TemplateUpdated, Kind: s.kind.Name, TemplateID: t.ID, At: s.timestamp()})
	return t, nil
}

// DeleteTemplate removes a stored template. For kinds that protect their
// default template the default cannot be deleted (update it instead);
// otherwise deleting an override of the default restores the built-in.
func (s *Service) DeleteTemplate(ctx context.Context, id string) error {
	if err := checkTemplateID(id); err != nil {
		return err
	}
	if id == render.DefaultTemplateID && s.kind.ProtectDefaultTemplate {
		return newErr(KindConflict, "the default %s template cannot be deleted; update it instead", s.kind.Name)
	}
	err := s.palace.store.DeleteTemplate(ctx, s.kind.Name, id)
	if errors.Is(err, store.ErrNotFound) {
		return newErr(KindNotFound, "%s template %q not found", s.kind.Name, id)
	}
	var fe *store.FanoutError
	if errors.As(err, &fe) {
		return &Error{Kind: KindStorage, Message: fe.Error(), Partial: true, Cause: err}
	}
	if err != nil {
		return storageErr(err)
	}
	s.palace.bus.Publish(events.Event{Type: events.TemplateDeleted, Kind: s.kind.Name, TemplateID: id, At: s.timestamp()})
	return nil
}
