// SPDX-License-Identifier: AGPL-3.0-only

package authz

import "github.com/radioBros/SimpleTickets/internal/domain"

// ArticleView is what a KB read decision needs to know about an article.
// CategoryVisibility is the visibility of the article's category (the
// narrowest one on its path); "" means it is unknown and only the article's
// own visibility is checked.
type ArticleView struct {
	Visibility         domain.KBVisibility
	CategoryVisibility domain.KBVisibility
	Status             domain.KBStatus
}

// ArticleAudience is the I-10 reader class of a principal.
type ArticleAudience int

const (
	ArticleAnonymous ArticleAudience = iota // no principal, or a token without kb:read
	ArticleCustomer
	ArticleStaff
)

// ArticleAudienceOf maps p to its I-10 class. A PAT without kb:read (which
// tickets:read implies) reads like an anonymous visitor.
func ArticleAudienceOf(p *domain.Principal) ArticleAudience {
	switch {
	case p == nil || !p.HasScope(domain.ScopeKBRead):
		return ArticleAnonymous
	case p.IsStaff():
		return ArticleStaff
	case p.IsCustomer():
		return ArticleCustomer
	}
	return ArticleAnonymous
}

// maxArticleRank is the widest domain.KBVisibilityRank the audience may read.
func (a ArticleAudience) maxArticleRank() int {
	switch a {
	case ArticleStaff:
		return domain.KBVisibilityRank(domain.KBInternal)
	case ArticleCustomer:
		return domain.KBVisibilityRank(domain.KBCustomers)
	}
	return domain.KBVisibilityRank(domain.KBPublic)
}

// CanSeeArticle implements I-10 (permissions_spec §2) as a pure function:
// anonymous readers see `public` articles, customers `public` and
// `customers`, staff everything including `internal`. Drafts and archived
// articles are staff-only (the public detail route answers 410 for an
// archived article the reader could otherwise see, without its content).
// The category's visibility narrows the article's (M-10). The SQL twin is
// the KB module's visibility predicate; TestCanSeeArticle_AgreesWithKBPredicate
// (internal/kb) keeps the two in agreement on both engines.
func CanSeeArticle(p *domain.Principal, a ArticleView) bool {
	aud := ArticleAudienceOf(p)
	if aud != ArticleStaff && a.Status != domain.KBPublished {
		return false
	}
	limit := aud.maxArticleRank()
	r := domain.KBVisibilityRank(a.Visibility)
	if r < 0 || r > limit {
		return false
	}
	if a.CategoryVisibility != "" {
		if c := domain.KBVisibilityRank(a.CategoryVisibility); c < 0 || c > limit {
			return false
		}
	}
	return true
}
