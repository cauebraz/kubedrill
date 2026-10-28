package cli

import (
	"github.com/cauebraz/kubedrill/internal/curriculum"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/progress"
	"github.com/cauebraz/kubedrill/internal/web"
)

// badgeBook holds the badge of every module, level and exam track of the
// curriculum, keyed by their ids, from the written scenarios of the catalog
// and the episodes.
type badgeBook struct {
	modules map[string]progress.Badge
	levels  map[string]progress.Badge
	tracks  map[string]progress.Badge
}

// badgesOf computes the badges of c. A scenario the curriculum names but
// the catalog lacks counts as planned, as it does in the catalog view.
func badgesOf(c *curriculum.Curriculum, cat Catalog, eps []journal.Episode) badgeBook {
	book := badgeBook{modules: map[string]progress.Badge{}, levels: map[string]progress.Badge{}, tracks: map[string]progress.Badge{}}
	standings := map[string]progress.Standing{}
	standing := func(id string) (progress.Standing, bool) {
		if id == "" {
			return progress.Standing{}, false
		}
		if s, ok := standings[id]; ok {
			return s, true
		}
		sc, err := cat.Find(id)
		if err != nil {
			return progress.Standing{}, false
		}
		s := progress.StandingOf(episodesOf(eps, sc.Meta.ID), scenarioInfo(sc))
		standings[id] = s
		return s, true
	}
	for _, lv := range c.Levels {
		mods := make([]progress.Badge, 0, len(lv.Modules))
		for _, m := range lv.Modules {
			var in []progress.Standing
			for _, ps := range m.Scenarios {
				if s, ok := standing(ps.ID); ok {
					in = append(in, s)
				}
			}
			b := progress.ModuleBadge(in)
			book.modules[m.ID] = b
			mods = append(mods, b)
		}
		book.levels[lv.ID] = progress.LevelBadge(mods)
	}
	for _, e := range c.Exams {
		cov, err := c.ExamCoverage(e.ID, nil)
		if err != nil {
			continue
		}
		domains := make([][]progress.Standing, 0, len(cov))
		for _, d := range cov {
			var in []progress.Standing
			for _, id := range d.Scenarios {
				if s, ok := standing(id); ok {
					in = append(in, s)
				}
			}
			domains = append(domains, in)
		}
		book.tracks[e.ID] = progress.TrackBadge(domains)
	}
	return book
}

// badgeWord is the state of b in the words of kubedrill path.
func badgeWord(b progress.Badge) string {
	if b.State == progress.BadgeInProgress {
		return "in progress"
	}
	return string(b.State)
}

// webBadge is b as the page reads it, with code in its center.
func webBadge(b progress.Badge, code string) web.Badge {
	return web.Badge{State: string(b.State), Lit: b.Lit, Clean: b.Clean, Total: b.Total, Pips: b.Pips, Variants: b.Variants, Caption: b.Caption, Code: code, EarnedAt: b.EarnedAt}
}
