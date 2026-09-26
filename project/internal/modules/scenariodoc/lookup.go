package scenariodoc

func issueByID(doc *Document, id string) *Issue {
	for i := range doc.Issues {
		if doc.Issues[i].ID == id {
			return &doc.Issues[i]
		}
	}
	return nil
}

func factByID(doc *Document, id string) *Fact {
	for i := range doc.Facts {
		if doc.Facts[i].ID == id {
			return &doc.Facts[i]
		}
	}
	return nil
}

func finalByID(doc *Document, id string) *Final {
	for i := range doc.Finals {
		if doc.Finals[i].ID == id {
			return &doc.Finals[i]
		}
	}
	return nil
}
