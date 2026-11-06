package paneltest

import (
	"github.com/artemlavr/giocondo/internal/app/panelapp"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	hostrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/hosts"
	networkrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/network"
	noderepo "github.com/artemlavr/giocondo/internal/repository/sqlite/nodes"
	proberepo "github.com/artemlavr/giocondo/internal/repository/sqlite/probes"
	settingsrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/settings"
	tokenrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/tokens"
	trafficrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/traffic"
	userrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/users"
)

// NodeRepository is the node storage port the application uses; fixtures wrap it to inject faults.
type NodeRepository = panelapp.NodeRepository

// Store is a panel database with its domain repositories, for fixtures that seed and inspect state.
type Store struct {
	DB       *sqlite.DB
	Users    *userrepo.Repository
	Tokens   *tokenrepo.Repository
	Nodes    *noderepo.Repository
	Network  *networkrepo.Repository
	Traffic  *trafficrepo.Repository
	Probes   *proberepo.Repository
	Hosts    *hostrepo.Repository
	Settings *settingsrepo.Repository
}

// NewStore returns the domain repositories over db.
func NewStore(db *sqlite.DB) *Store {
	return &Store{
		DB:       db,
		Users:    userrepo.New(db),
		Tokens:   tokenrepo.New(db),
		Nodes:    noderepo.New(db),
		Network:  networkrepo.New(db),
		Traffic:  trafficrepo.New(db),
		Probes:   proberepo.New(db),
		Hosts:    hostrepo.New(db),
		Settings: settingsrepo.New(db),
	}
}
