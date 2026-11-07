package short_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rukins/gore/internal/db"
	"github.com/rukins/gore/internal/models"
	"github.com/rukins/gore/internal/short"
)

// fakeStore holds one link, so an update can be checked against what it
// started as.
type fakeStore struct {
	db.Store
	stored  *models.Link
	updated *models.Link
}

func (s *fakeStore) FetchAlias(context.Context, string, string) (*models.Link, error) {
	c := *s.stored
	return &c, nil
}

func (s *fakeStore) UpdateAlias(_ context.Context, r *models.Link) error {
	c := *r
	s.updated = &c
	return nil
}

func (s *fakeStore) StoreAlias(_ context.Context, r *models.Link) error {
	c := *r
	s.updated = &c
	return nil
}

// TestUpdateKeepsUnmentionedFields is a regression test for a bug the
// code carried a FIXME about.
//
// A boolean flag that was not passed is false, which is indistinguishable
// from one passed as false. An update read that as an instruction, so
// `gore -op update -a x -l <new url>` also made a private link public
// and a trusted link warn before redirecting. Nothing said so.
func TestUpdateKeepsUnmentionedFields(t *testing.T) {
	held := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &fakeStore{stored: &models.Link{
		ID: "1", Alias: "x", URL: "https://old.example",
		Private: true, Trust: true, ValidFrom: held,
	}}

	// Only the URL is given, as `-op update -a x -l ...` supplies.
	err := short.Edit(context.Background(), s, short.OpUpdate, "x",
		&models.Link{Alias: "x", URL: "https://new.example"},
		short.Given{URL: true})
	if err != nil {
		t.Fatal(err)
	}

	if s.updated.URL != "https://new.example" {
		t.Errorf("URL = %q, want the new one", s.updated.URL)
	}
	if !s.updated.Private {
		t.Error("a private link was made public by an update that said nothing about it")
	}
	if !s.updated.Trust {
		t.Error("a trusted link was made to warn by an update that said nothing about it")
	}
	if !s.updated.ValidFrom.Equal(held) {
		t.Errorf("ValidFrom = %v, want the stored %v", s.updated.ValidFrom, held)
	}
}

// TestUpdateAppliesWhatIsGiven checks the other direction: a field that
// was named takes effect, including when its value is the zero one.
func TestUpdateAppliesWhatIsGiven(t *testing.T) {
	s := &fakeStore{stored: &models.Link{
		ID: "1", Alias: "x", URL: "https://old.example",
		Private: true, Trust: true,
	}}

	err := short.Edit(context.Background(), s, short.OpUpdate, "x",
		&models.Link{Alias: "x", Private: false, Trust: false},
		short.Given{Private: true, Trust: true})
	if err != nil {
		t.Fatal(err)
	}

	if s.updated.Private {
		t.Error("-p was given as false and did not take effect")
	}
	if s.updated.Trust {
		t.Error("-trust was given as false and did not take effect")
	}
	if s.updated.URL != "https://old.example" {
		t.Errorf("URL = %q, want the stored one", s.updated.URL)
	}
}

// createStore records every insert. fail is how many of them are refused
// as already taken, so a retry can be told from a single draw.
type createStore struct {
	db.Store
	fail    int
	stored  []*models.Link
	updated *models.Link
}

func (s *createStore) StoreAlias(_ context.Context, r *models.Link) error {
	c := *r
	s.stored = append(s.stored, &c)
	if s.fail > 0 {
		s.fail--
		return db.ErrAliasExists
	}
	return nil
}

func (s *createStore) FetchAlias(context.Context, string, string) (*models.Link, error) {
	return nil, errors.New("missing")
}

func (s *createStore) UpdateAlias(_ context.Context, r *models.Link) error {
	c := *r
	s.updated = &c
	return nil
}

func TestCreateWithoutAliasAssignsOne(t *testing.T) {
	for _, alias := range []string{"", " \t"} {
		s := &createStore{}
		r := &models.Link{Alias: alias, URL: "https://example.com", UpdatedBy: "ada"}
		err := short.Edit(context.Background(), s, short.OpCreate, "", r, short.Given{})
		if err != nil {
			t.Fatal(err)
		}
		if len(s.stored) != 1 {
			t.Fatalf("alias %q: stored %d links, want 1", alias, len(s.stored))
		}
		got := s.stored[0]
		if len(got.Alias) != 6 || !short.Validity.MatchString(got.Alias) {
			t.Fatalf("alias = %q, want 6 valid characters", got.Alias)
		}
		if got.Alias != r.Alias {
			t.Fatalf("returned %q, stored %q", r.Alias, got.Alias)
		}
		if got.CreatedBy != "ada" {
			t.Errorf("CreatedBy = %q, want ada", got.CreatedBy)
		}
	}
}

func TestCreateRetriesATakenAlias(t *testing.T) {
	s := &createStore{fail: 1}
	r := &models.Link{URL: "https://example.com", UpdatedBy: "ada"}
	err := short.Edit(context.Background(), s, short.OpCreate, "", r, short.Given{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.stored) != 2 {
		t.Fatalf("stored %d draws, want the collision and the retry", len(s.stored))
	}
	if s.stored[0].Alias == s.stored[1].Alias {
		t.Fatalf("retried %q, the same draw", s.stored[0].Alias)
	}
	if r.Alias != s.stored[1].Alias {
		t.Fatalf("kept %q, want the second draw %q", r.Alias, s.stored[1].Alias)
	}
}

func TestCreateRejectsInvalidAlias(t *testing.T) {
	s := &createStore{}
	err := short.Edit(context.Background(), s, short.OpCreate, "",
		&models.Link{Alias: "!"}, short.Given{})
	if !errors.Is(err, short.ErrInvalidAlias) {
		t.Fatalf("err = %v, want ErrInvalidAlias", err)
	}
	if s.stored != nil {
		t.Fatalf("stored %v for an invalid alias", s.stored)
	}
}

func TestUpdateWithoutAliasDoesNotStore(t *testing.T) {
	s := &createStore{}
	r := &models.Link{}
	err := short.Edit(context.Background(), s, short.OpUpdate, "", r, short.Given{})
	if err == nil {
		t.Fatal("update with no alias succeeded")
	}
	if s.stored != nil || s.updated != nil {
		t.Fatalf("wrote a link: stored %v updated %v", s.stored, s.updated)
	}
	if r.Alias != "" {
		t.Fatalf("alias = %q, an update minted one", r.Alias)
	}
}
