http.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
  query := r.URL.Query().Get("q")
  searchRes, err := client.Index("movies").Search(query, &meilisearch.SearchRequest{})
  if err != nil {
    http.Error(w, err.Error(), http.StatusInternalServerError)
    return
  }
  json.NewEncoder(w).Encode(searchRes.Hits)
})
