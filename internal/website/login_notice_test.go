package website

import (
 "net/http"
 "net/url"
 "strings"
 "testing"

 "possums-intake/internal/failuretype"
)

func TestInvalidCredentialsUseSimpleNoticeAndKeepClassification(t *testing.T) {
 f:=newAccountFixture(t)
 _,body:=f.request(t,"GET","/login",nil,"")
 form:=url.Values{"csrf_token":{hidden(t,body,"csrf_token")},"revision":{hidden(t,body,"revision")},"key":{strings.Repeat("D",128)}}
 before:=len(f.observer.counts())
 response,body:=f.request(t,"POST","/login",form,f.server.URL)
 counts:=f.observer.counts()
 if response.StatusCode!=http.StatusUnauthorized || !strings.Contains(body,`<span id="form-error" role="alert">Invalid credentials. Please try again.</span>`) || len(counts)!=before+1 || counts[len(counts)-1]!=failuretype.AuthenticationRefused {
  t.Fatal("SITE_TEST_SIMPLE_LOGIN_NOTICE_CLASSIFICATION")
 }
 if strings.Index(body,`id="form-error"`)<=strings.Index(body,"</form>") || !strings.Contains(body,`aria-describedby="form-error"`) {
  t.Fatal("SITE_TEST_SIMPLE_LOGIN_NOTICE_POSITION")
 }
 for _,forbidden:=range []string{form.Get("key"),"SITE_AUTH_REFUSED","stage=","constraint=","outcome="} {
  if strings.Contains(body,forbidden){t.Fatal("SITE_TEST_SIMPLE_LOGIN_NOTICE_PRIVACY")}
 }
 response,_=f.request(t,"GET","/home",nil,"")
 if response.StatusCode!=http.StatusSeeOther {t.Fatal("SITE_TEST_REFUSED_LOGIN_GRANTED_SESSION")}
 if accountLogin(t,f.browser,keyHTTPA)!=http.StatusSeeOther {t.Fatal("SITE_TEST_VALID_LOGIN_AFTER_REFUSAL")}
}
