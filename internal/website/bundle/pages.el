(defun public-home (view preferences)
  "A quiet public introduction."
  (page "Possums"
    (ui:group
      (ui:p "Private inference, with clear boundaries.")
      (ui:text "\n")
      (ui:link (site:url 'login) "Sign in")
      (ui:text "\n"))))

(defun login-page (view preferences)
  "Render the host-owned sign-in form without exposing credentials to Lisp."
  (page "Sign in"
    (ui:group
      (ui:notice "\n")
      (ui:login-form 'sign-in)
      (ui:text "\n")
      (ui:face 'tooltip "Sign-in requires first-party cookies. Tor New Identity or restarting your browser may end your session.")
      (ui:text "\n")
      (ui:link (site:url 'public-home) "Back to Possums")
      (ui:text "\n"))))

(defun signed-home (view preferences)
  "Render only the host-owned scoped snapshot; Lisp performs no accounting."
  (page "Home"
    (ui:group
      (home-introduction preferences)
      (ui:text (concat 'string (get view "account-snapshot")))
      (ui:notice "\n")
      (ui:post-form 'sign-out "Sign out")
      (ui:text "\n"))))
