(defun front (v p) (page "Example" (ui:group (ui:p "Example text") (ui:link (site:url 'details) "Details") (ui:face 'tooltip "Hint"))))
(defun details (v p) (page "Details" (ui:group (ui:p "Details text") (ui:post-form 'clear "Clear"))))
(defun form-page (v p) (page "Form" (ui:group (ui:notice "\n") (ui:login-form 'send))))
