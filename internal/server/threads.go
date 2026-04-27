package server

import (
	"net/http"

	"zora/internal/threads"
)

type threadsListData struct {
	AppName    string
	Nav        navState
	UserName   string
	Groups     []threads.KindGroup
	TotalCount int
}

type threadDetailData struct {
	AppName   string
	Nav       navState
	UserName  string
	Thread    threads.Thread
	KindLabel string
	DateRange string
	Members   []threadMemberView
	Facts     []threads.FactSummary
	NotFound  bool
}

type threadMemberView struct {
	Member  threads.Member
	DueChip string
}

func (s *Server) threadList(w http.ResponseWriter, r *http.Request) {
	repo := threads.Repository{DB: s.database}
	list, err := repo.RecentThreads(r.Context(), 200)
	if err != nil {
		s.logger.Error("list threads", "error", err)
		http.Error(w, "list threads", http.StatusInternalServerError)
		return
	}
	data := threadsListData{
		AppName:    "Zora",
		Nav:        navFor(r.URL.Path),
		UserName:   s.cfg.User.DisplayName,
		Groups:     threads.GroupByKind(list),
		TotalCount: len(list),
	}
	s.render(w, "threads.html", data)
}

func (s *Server) threadDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	repo := threads.Repository{DB: s.database}
	thread, ok, err := repo.GetThread(r.Context(), id)
	if err != nil {
		s.logger.Error("get thread", "error", err)
		http.Error(w, "get thread", http.StatusInternalServerError)
		return
	}
	if !ok {
		s.renderStatus(w, "thread.html", http.StatusNotFound, threadDetailData{
			AppName:  "Zora",
			Nav:      navFor(r.URL.Path),
			UserName: s.cfg.User.DisplayName,
			NotFound: true,
		})
		return
	}
	members, err := repo.ListMembers(r.Context(), id)
	if err != nil {
		s.logger.Error("list thread members", "error", err)
		http.Error(w, "list thread members", http.StatusInternalServerError)
		return
	}
	facts, err := repo.ThreadFacts(r.Context(), id)
	if err != nil {
		s.logger.Error("thread facts", "error", err)
		http.Error(w, "thread facts", http.StatusInternalServerError)
		return
	}
	views := make([]threadMemberView, 0, len(members))
	for _, m := range members {
		views = append(views, threadMemberView{
			Member:  m,
			DueChip: dueChipClass(m.EventAt),
		})
	}
	data := threadDetailData{
		AppName:   "Zora",
		Nav:       navFor(r.URL.Path),
		UserName:  s.cfg.User.DisplayName,
		Thread:    thread,
		KindLabel: humaneThreadKind(thread.Kind),
		DateRange: humaneDateRange(thread.DateStart, thread.DateEnd),
		Members:   views,
		Facts:     facts,
	}
	s.render(w, "thread.html", data)
}
