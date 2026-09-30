package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func TestNotificationOwners(t *testing.T) {
	ctx := context.Background()
	s := store.NewNotificationOwners(testdb.New(t))

	o := model.NotificationOwner{Name: " Ali ", Email: ptr(" Ali@X.test "), Phone: ptr("+90 (555) 000-00-01"), EmailEnabled: true, SMSEnabled: true}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	if o.Name != "Ali" || *o.Email != "ali@x.test" || *o.Phone != "+905550000001" {
		t.Fatalf("normalized = %+v %s %s", o, *o.Email, *o.Phone)
	}
	ali, err := s.Create(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	noc := model.NotificationOwner{Name: "NOC", Email: ptr("noc@x.test"), Phone: ptr(""), EmailEnabled: true}
	if err := noc.Normalize(); err != nil || noc.Phone != nil {
		t.Fatalf("empty phone: %+v err=%v, want nil", noc.Phone, err)
	}
	if _, err := s.Create(ctx, noc); err != nil {
		t.Fatal(err)
	}

	for name, bad := range map[string]model.NotificationOwner{
		"no name":         {Name: "  ", Email: ptr("a@x.test")},
		"no address":      {Name: "Boş", Email: ptr(""), Phone: ptr(" ")},
		"display name":    {Name: "X", Email: ptr("Ali <ali@x.test>")},
		"letters phone":   {Name: "X", Phone: ptr("+90 555 ABC")},
		"too short phone": {Name: "X", Phone: ptr("12345")},
	} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// Aynı e-posta ikinci kez eklenemez.
	dup := model.NotificationOwner{Name: "Kopya", Email: ptr("ali@x.test")}
	if _, err := s.Create(ctx, dup); !errors.Is(err, store.ErrOwnerEmailTaken) {
		t.Fatalf("duplicate e-mail: err=%v", err)
	}

	ali.SMSEnabled, ali.Phone = false, nil
	upd, err := s.Update(ctx, ali)
	if err != nil || upd.SMSEnabled || upd.Phone != nil || !upd.UpdatedAt.After(ali.UpdatedAt) {
		t.Fatalf("update: %+v err=%v", upd, err)
	}
	if list, err := s.List(ctx); err != nil || len(list) != 2 || list[0].Name != "Ali" || list[1].Name != "NOC" {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	if err := s.Delete(ctx, ali.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ali.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get after delete: err=%v", err)
	}
	if _, err := s.Update(ctx, model.NotificationOwner{ID: uuid.New(), Name: "x", Email: ptr("x@x.test")}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update unknown: err=%v", err)
	}
	if err := s.Delete(ctx, ali.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: err=%v", err)
	}
}
