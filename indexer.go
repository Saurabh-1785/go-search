package gosearch

type Movie struct {
  ID     int    `json:"id"`
  Title  string `json:"title"`
  Genre  string `json:"genre"`
  Url    string `json:"url"`
  Timestamp string `json:"timestamp"`
}

movies := []Movie{
  {ID: 1, Title: "Inception", Genre: "Sci-Fi"},
  {ID: 2, Title: "The Matrix", Genre: "Action"},
}

res, err := client.Index("movies").AddDocuments(movies)
if err != nil {
  log.Fatal(err)
}
