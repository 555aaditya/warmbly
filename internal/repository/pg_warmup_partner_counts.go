package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/models"
)

// WarmupPartnerCounts shares cohort scans while retaining each sender's eligibility and exclusions.
func (r *warmupRepository) WarmupPartnerCounts(ctx context.Context, poolType string, senderIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(senderIDs))
	if len(senderIDs) == 0 {
		return out, nil
	}
	for _, id := range senderIDs {
		out[id] = 0
	}
	orgs, err := r.eligiblePartnerSenders(ctx, poolType, senderIDs)
	if err != nil || len(orgs) == 0 {
		return out, err
	}
	suffix := candidateSuffixFor(poolType)
	own, err := r.queryPartnerCandidates(ctx, `
		SELECT wpp.email_account_id AS id, ea.email, ea.organization_id, ea.provider::text AS provider, ea.mail_host
		FROM warmup_pool_participants wpp
		JOIN warmup_pools wp ON wpp.pool_id = wp.id
		JOIN email_accounts ea ON ea.id = wpp.email_account_id
		WHERE wp.pool_type = $1 AND `+partnerEligibleSQL,
		suffix, poolType, models.WarmupPartnerOwnTier, poolType)
	if err != nil {
		return nil, err
	}
	ownIDs := make(map[uuid.UUID]bool, len(own))
	for _, c := range own {
		ownIDs[c.ID] = true
	}
	for id := range orgs {
		out[id] = len(own)
		if ownIDs[id] {
			out[id]--
		}
	}
	if borrowFrom, ok := models.WarmupPoolBorrowsFrom(poolType); ok {
		borrowed, err := r.queryPartnerCandidates(ctx, `
			SELECT wpp.email_account_id AS id, ea.email, ea.organization_id, ea.provider::text AS provider, ea.mail_host
			FROM warmup_pool_participants wpp
			JOIN warmup_pools wp ON wpp.pool_id = wp.id
			JOIN email_accounts ea ON ea.id = wpp.email_account_id
			JOIN organizations o ON o.id = ea.organization_id
			WHERE wp.pool_type = $1 AND `+partnerEligibleSQL+` AND `+partnerProvenSQL,
			suffix, borrowFrom, models.WarmupPartnerBorrowed, borrowFrom, config.WarmupPoolFallbackMinAgeDays)
		if err != nil {
			return nil, err
		}
		byOrg := make(map[uuid.UUID]int)
		for _, c := range borrowed {
			if c.OrganizationID != nil {
				byOrg[*c.OrganizationID]++
			}
		}
		for id, org := range orgs {
			out[id] += len(borrowed) - byOrg[org]
		}
	}
	if returnTo, ok := models.WarmupPoolReturnsTo(poolType); ok {
		returns, err := r.queryPartnerCandidates(ctx, `
			SELECT wpp.email_account_id AS id, ea.email, ea.organization_id, ea.provider::text AS provider, ea.mail_host
			FROM warmup_pool_participants wpp
			JOIN warmup_pools wp ON wpp.pool_id = wp.id
			JOIN email_accounts ea ON ea.id = wpp.email_account_id
			WHERE wp.pool_type = $1 AND `+partnerEligibleSQL+`
			  AND wpp.email_account_id IN (
			      SELECT wr.sender_account_id FROM warmup_received wr
			      WHERE wr.email_account_id = ANY($2::uuid[])
			        AND wr.created_at >= NOW() - make_interval(days => $3))`,
			suffix, returnTo, models.WarmupPartnerReturn, returnTo, senderIDs, config.WarmupPoolReturnVisitDays)
		if err != nil {
			return nil, err
		}
		returnIDs := make(map[uuid.UUID]bool, len(returns))
		for _, c := range returns {
			returnIDs[c.ID] = true
		}
		rows, err := resultDB(ctx, r.db).Query(ctx, `
			SELECT DISTINCT ea.id, wr.sender_account_id
			FROM warmup_received wr
			JOIN email_accounts ea ON ea.id = wr.email_account_id
			JOIN warmup_pool_participants wpp ON wpp.email_account_id = ea.id
			JOIN warmup_pools wp ON wp.id = wpp.pool_id
			JOIN organizations o ON o.id = ea.organization_id
			WHERE ea.id = ANY($3::uuid[]) AND wp.pool_type = $1 AND ea.status = 'active'
			  AND `+partnerProvenSQL+`
			  AND wr.created_at >= NOW() - make_interval(days => $4)`,
			poolType, config.WarmupPoolFallbackMinAgeDays, senderIDs, config.WarmupPoolReturnVisitDays)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var sender, partner uuid.UUID
			if err := rows.Scan(&sender, &partner); err != nil {
				return nil, err
			}
			if _, eligible := orgs[sender]; eligible && returnIDs[partner] {
				out[sender]++
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *warmupRepository) eligiblePartnerSenders(ctx context.Context, poolType string, senderIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	rows, err := resultDB(ctx, r.db).Query(ctx, `
		SELECT ea.id, ea.organization_id
		FROM warmup_pool_participants wpp
		JOIN warmup_pools wp ON wp.id = wpp.pool_id
		JOIN email_accounts ea ON ea.id = wpp.email_account_id
		WHERE ea.id = ANY($1::uuid[]) AND wp.pool_type = $2
		  AND wpp.participant_role = 'sender_receiver'
		  AND `+testSenderSQL+` AND `+poolEligibleSQL, senderIDs, poolType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]uuid.UUID)
	for rows.Next() {
		var id, org uuid.UUID
		if err := rows.Scan(&id, &org); err != nil {
			return nil, err
		}
		out[id] = org
	}
	return out, rows.Err()
}
