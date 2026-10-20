(defgroup home "Choose how your home is presented.")

(defcustom home-show-introduction true
  "Show the short introduction on your home page."
  :type 'boolean :group 'home)

(defun home-introduction (preferences)
  "Render the introduction when the declared option enables it."
  (if (site:option preferences 'home-show-introduction)
      (ui:group (ui:p "You are signed in.") (ui:text "\n"))
      (ui:group)))
