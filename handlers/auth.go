package handlers

import (
	"net/http"

	"checkout/config"
	"checkout/templates"
	"checkout/utils"
)

// Authentication middleware
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip authentication for login page and static assets
		if r.URL.Path == "/login" || r.URL.Path == "/static/" || r.URL.Path == "/static/css/styles.css" {
			next.ServeHTTP(w, r)
			return
		}

		// Check if authenticated
		cookie, err := r.Cookie("auth")
		if err != nil || cookie.Value != "authenticated" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// LoginHandler handles the login page
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Error parsing form", http.StatusBadRequest)
			return
		}

		if r.FormValue("password") == config.Config.Password {
			// Set authentication cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "auth",
				Value:    "authenticated",
				Path:     "/",
				MaxAge:   3600 * 8, // 8 hours
				HttpOnly: true,
			})

			// For HTMX requests, we need to set specific headers to ensure proper redirection
			// Skip any target processing entirely to prevent content from loading in the error div
			w.Header().Set("HX-Redirect", "/")

			// Return immediately with an empty response to ensure HTMX processes the redirect
			// before attempting to process any response body
			w.WriteHeader(http.StatusOK)
			return
		}

		// Wrong PIN - direct error message in the target element
		// Using HTTP 200 status because HTMX only processes successful responses for DOM insertion by default
		// The error is communicated to the user through the response content, not the HTTP status code
		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte(`<div class="error-message">Invalid password. Please try again.</div>`)); err != nil {
			utils.Error("auth", "Error writing error message to response", "error", err)
		}
		return
	}

	// Check if already logged in
	cookie, err := r.Cookie("auth")
	if err == nil && cookie.Value == "authenticated" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Display login page using templ
	component := templates.LoginPage()
	err = component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// LogoutHandler handles user logout
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	// Clear authentication cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	// For HTMX requests, use HX-Redirect header instead of HTTP redirect
	// This ensures proper client-side navigation without content being injected into the wrong place
	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}
