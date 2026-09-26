package httpx

import (
	"net/http"
	"strconv"

	"arena-portal-backend/internal/api/gen"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// ParsePagination разбирает ?limit=&offset= (arena-portal-hr.md 8.1): limit
// по умолчанию 50, не больше 200; offset по умолчанию 0. Неразборчивое или
// отрицательное значение — не отказ, а сползание к значению по умолчанию:
// пагинация — это подсказка клиенту, а не проверка допуска.
func ParsePagination(r *http.Request) (limit, offset int) {
	limit = parseBoundedInt(r.URL.Query().Get("limit"), defaultLimit, 1, maxLimit)
	offset = parseBoundedInt(r.URL.Query().Get("offset"), 0, 0, int(^uint(0)>>1))
	return limit, offset
}

// Pagination — то же, что ParsePagination, но для параметров, которые
// сгенерированный strict-сервер уже разобрал в *int.
func Pagination(limit, offset *int) (int, int) {
	l, o := defaultLimit, 0
	if limit != nil {
		l = min(max(*limit, 1), maxLimit)
	}
	if offset != nil {
		o = max(*offset, 0)
	}
	return l, o
}

func parseBoundedInt(raw string, def, min, max int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// PageMeta строит page-конверт ответа {total, limit, offset}.
func PageOf(total, limit, offset int) gen.PageMeta {
	return gen.PageMeta{Total: total, Limit: limit, Offset: offset}
}

// RepeatedParam читает фильтр, заданный повтором параметра (arena-portal-hr.md
// 8.1: «несколько значений — повтором параметра»), например
// ?group_id=a&group_id=b.
func RepeatedParam(r *http.Request, name string) []string {
	values := r.URL.Query()[name]
	if values == nil {
		return nil
	}
	return values
}
