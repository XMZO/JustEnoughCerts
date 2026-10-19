//go:build !no_calendar

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/calendar"

func init() { modules = append(modules, calendar.New()) }
