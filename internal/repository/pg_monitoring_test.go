package repository

import (
	"strings"
	"testing"
)

func TestMonitoringStatementUsesOneDatabaseObservation(t *testing.T) {
	for id, section := range monitoringSections {
		t.Run(id, func(t *testing.T) {
			query := monitoringStatement(section)
			if !strings.HasPrefix(query, "WITH observation AS MATERIALIZED (SELECT clock_timestamp() AS at) ") || strings.Count(query, "clock_timestamp()") != 1 || strings.Contains(query, "$1") || strings.Count(query, " UNION ALL ") != len(section.queries)-1 {
				t.Fatal("source lost its single statement/database reference", query)
			}
			for _, metric := range section.queries {
				if !strings.Contains(query, "SELECT '"+metric.id+"', (SELECT at FROM observation), ") {
					t.Fatal("metric does not return the source reference", metric.id)
				}
			}
		})
	}
}
