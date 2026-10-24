(defun view-text (view key)
  "Missing view entries remain safe for legacy empty-view preparation."
  (let ((value (get view key))) (if value value "")))

(defun view-usd (view key)
  "An absent snapshot is unavailable, never a fabricated zero."
  (let ((value (get view key)))
    (if value (concat 'string value " USD") "unavailable")))

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
      (ui:login-form 'sign-in)
      (ui:text "\n")
      (ui:notice "\n")
      (ui:face 'tooltip "Sign-in requires first-party cookies. Tor New Identity or restarting your browser may end your session.")
      (ui:text "\n")
      (ui:link (site:url 'public-home) "Back to Possums")
      (ui:text "\n"))))

(defun signed-home (view preferences)
  "Render only the host-owned scoped snapshot; Lisp performs no accounting."
  (page "Home"
    (ui:group
      (home-introduction preferences)
      (ui:p (concat 'string "Balance: " (view-usd view "balance-usd")))
      (ui:text "\n")
      (ui:p (concat 'string "Available: " (view-usd view "available-usd")))
      (ui:text "\n")
      (ui:host 'topup-link)
      (ui:text "\n")
      (ui:text (view-text view "account-snapshot"))
      (ui:text "\n")
      (ui:notice "\n")
      (ui:post-form 'sign-out "Sign out")
      (ui:text "\n"))))

(defun payment-status-text (state)
  "Presentation only; bounded host strings never establish new funding evidence."
  (let ((copy (get (sorted-map
    "form" "Choose one fixed credit amount. No subscription or automatic reload."
    "funded" "Credit was added once."
    "awaiting_payment" "A Checkout session was created; payment and account funding are not confirmed."
    "support_required" "Automatic funding is blocked. Contact the operator; this is not confirmation of a refund."
    "unavailable_after_unlink" "The payment link is unavailable. This does not establish whether a charge or credit occurred."
    "no-reference" "No purchase reference is available in this browser session. This does not establish whether a payment occurred.") state)))
    (if copy copy
      "Payment creation or funding is unconfirmed. No creation attempt was replayed. A deliberate new purchase may create another payment.")))

(defun topup-balance (view)
  "A current account snapshot, independent of the original payment receipt."
  (if (get (sorted-map "available" true) (view-text view "balance-state"))
      (ui:group
        (ui:p (concat 'string "Current balance: " (view-usd view "balance-usd")))
        (ui:text "\n")
        (ui:p (concat 'string "Available: " (view-usd view "available-usd")))
        (ui:text "\n"))
      (ui:group
        (ui:p "Current balance is unavailable. Open your account page or check again; your payment was not retried.")
        (ui:text "\n"))))

(defun payment-notice (view)
  "Only the closed host mode selects live copy; empty legacy views stay test-only."
  (if (get (sorted-map "live" true) (view-text view "payment-mode"))
      (ui:group
        (ui:p "Payments are live. The selected USD amount becomes inference credit; tax is added at Stripe Checkout and does not become credit.")
        (ui:text "\n\n")
        (ui:p "Stripe processes payment and tax details and keeps its own records. Card and tax-address details are not stored in our local sales records.")
        (ui:text "\n\n")
        (ui:p "Local purchase links and sales records share a deletion cutoff 20 days after checkout starts. Maintenance delays and storage remnants can retain data longer; this does not delete Stripe records."))
      (ui:p "Sandbox/test only. Use synthetic payment details; do not make real payments.")))

(defun topup-page (view preferences)
  "Compose the first-party payment page; all actions and hidden inputs stay host-owned."
  (let ((state (view-text view "payment-state")))
    (page "Add credit"
      (ui:group
        (payment-notice view)
        (ui:text "\n\n")
        (ui:p (payment-status-text state))
        (ui:text "\n")
        (if (get (sorted-map "form" true) state)
            (ui:group (ui:host 'topup-form) (ui:text "\n"))
            (topup-balance view))
        (ui:p "A new submission may create another payment even if an earlier outcome is unknown. Do not submit again to diagnose a missing receipt.")
        (ui:text "\n\n")
        (if (get (sorted-map "awaiting_payment" true "unknown" true) state)
            (ui:group (ui:host 'topup-status-link) (ui:text " · "))
            (ui:group))
        (ui:host 'account-link)
        (ui:text "\n")))))
